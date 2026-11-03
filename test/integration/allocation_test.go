package integration

import (
	"bytes"
	"context"
	be "encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	capCDP "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	capHTTP "github.com/ai-ecoverse/slicc-cli/internal/caps/http"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

func TestSharedHTTPAndCDPAllocations(t *testing.T) {
	for _, ending := range []string{"reset", "completion", "error", "disconnect"} {
		t.Run(ending, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			fake := newFakeCDPBrowser(t, func(string, json.RawMessage) (string, []string) { return "{}", nil })
			registry := capCDP.NewRegistry(nil)
			browser, err := registry.Add(ctx, fake.srv.URL, false)
			if err != nil {
				t.Fatal(err)
			}
			budget := link.NewAllocationBudget(2*capHTTP.MaxHead + 4096 + 1024)
			pair := publicationLinks(t, ctx, link.Options{
				AllocationBudget: budget,
				Hello:            link.Hello{Caps: []string{"http", "cdp"}},
				Handlers:         map[string]link.Handler{"http": capHTTP.Handler(capHTTP.Options{Dialer: &net.Dialer{Timeout: time.Second}}), "cdp": registry.Handler()},
			}, link.Options{})
			open := func(kind string) *link.Stream {
				t.Helper()
				s, e := pair.page.Open(ctx, map[string]any{"kind": kind, "browser": browser.ID}, link.OpenOptions{Shared: true})
				if e != nil {
					t.Fatal(e)
				}
				return s
			}
			cdp := open("cdp")
			h1, h2 := open("http"), open("http")
			_, err = pair.page.Open(ctx, map[string]any{"kind": "http"}, link.OpenOptions{Shared: true})
			var reset *link.ResetError
			if !errors.As(err, &reset) || reset.Code != "ENOBUFS" {
				t.Fatalf("HTTP admission: %v", err)
			}
			_, err = pair.page.Open(ctx, map[string]any{"kind": "cdp", "browser": browser.ID}, link.OpenOptions{Shared: true})
			if !errors.As(err, &reset) || reset.Code != "ENOBUFS" {
				t.Fatalf("CDP admission: %v", err)
			}
			var size [4]byte
			be.BigEndian.PutUint32(size[:], capHTTP.MaxHead)
			if _, err = cdp.Write(size[:]); err != nil {
				t.Fatal(err)
			}
			payload := bytes.Repeat([]byte{' '}, capHTTP.MaxHead)
			copy(payload, []byte(`{"id":1,"method":"Browser.getVersion"}`))
			written := make(chan error, 1)
			go func() { _, e := cdp.Write(payload); written <- e }()
			select {
			case err = <-written:
				t.Fatalf("CDP exceeded its granted window while reservation exhausted: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			if err = h1.Reset("ECONNRESET"); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-written:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("CDP did not resume after HTTP released its reservation")
			}
			var replySize [4]byte
			if _, err = io.ReadFull(cdp, replySize[:]); err != nil {
				t.Fatal(err)
			}
			reply := make([]byte, be.BigEndian.Uint32(replySize[:]))
			if _, err = io.ReadFull(cdp, reply); err != nil {
				t.Fatal(err)
			}
			switch ending {
			case "reset":
				_ = cdp.Reset("ECONNRESET")
				_ = h2.Reset("ECONNRESET")
			case "disconnect":
				_ = pair.page.Close()
			case "error":
				be.BigEndian.PutUint32(size[:], capCDP.MaxMessage+1)
				_, _ = cdp.Write(size[:])
				_, _ = h2.Write([]byte{0, 0, 0, 1, 'x'})
				_, _ = io.Copy(io.Discard, cdp)
				_, _ = io.Copy(io.Discard, h2)
			case "completion":
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
				defer upstream.Close()
				head, e := capHTTP.EncodeHead(capHTTP.RequestHead{URL: upstream.URL, Method: "GET", Headers: [][2]string{}})
				if e != nil {
					t.Fatal(e)
				}
				_, _ = h2.Write(head)
				_ = h2.CloseWrite()
				if _, e = io.Copy(io.Discard, h2); e != nil {
					t.Fatal(e)
				}
				_ = cdp.CloseWrite()
				if _, e = io.Copy(io.Discard, cdp); e != nil {
					t.Fatal(e)
				}
			}
			allocationZero(t, ctx, budget)
		})
	}
}

func allocationZero(t *testing.T, ctx context.Context, budget *link.AllocationBudget) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for budget.Used() != 0 {
		select {
		case <-ctx.Done():
			t.Fatalf("leaked reservation: %d", budget.Used())
		case <-ticker.C:
		}
	}
}

func TestSharedAllocationGuardHoldFailsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const secret = "protected-hold-content-must-not-reach-seven"
	event := `{"method":"Network.requestWillBeSentExtraInfo","params":{"requestId":"unseen","headers":{"Cookie":"` + secret + `"},"padding":"` + string(bytes.Repeat([]byte{'x'}, 128<<10)) + `"}}`
	fake := newFakeCDPBrowser(t, func(method string, _ json.RawMessage) (string, []string) {
		if method == "Browser.getVersion" {
			return "{}", []string{`{"method":"Network.responseReceivedExtraInfo","params":{"requestId":"held-before-failure","headers":{"Set-Cookie":"` + secret + `"}}}`, event}
		}
		return "{}", nil
	})
	registry := capCDP.NewRegistry(nil)
	browser, err := registry.Add(ctx, fake.srv.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	budget := link.NewAllocationBudget(2*capHTTP.MaxHead + (512 << 10))
	pair := publicationLinks(t, ctx, link.Options{AllocationBudget: budget, Hello: link.Hello{Caps: []string{"http", "cdp"}}, Handlers: map[string]link.Handler{
		"http": capHTTP.Handler(capHTTP.Options{Dialer: &net.Dialer{}}), "cdp": registry.Handler(),
	}}, link.Options{})
	open := func(kind string) *link.Stream {
		t.Helper()
		s, e := pair.page.Open(ctx, map[string]any{"kind": kind, "browser": browser.ID}, link.OpenOptions{Shared: true})
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	cdp := open("cdp")
	h1, h2 := open("http"), open("http")
	request := []byte(`{"id":1,"method":"Browser.getVersion"}`)
	var size [4]byte
	be.BigEndian.PutUint32(size[:], uint32(len(request)))
	if _, err = cdp.Write(append(size[:], request...)); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(cdp)
	var reset *link.ResetError
	if !errors.As(err, &reset) || reset.Code != "ENOBUFS" {
		t.Fatalf("guard hold did not fail closed: %q %v", data, err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatal("unchecked held content reached stream")
	}
	_ = h1.Reset("ECONNRESET")
	_ = h2.Reset("ECONNRESET")
	allocationZero(t, ctx, budget)
}

func TestSharedAllocationParkedCommandsRelease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fake := newFakeCDPBrowser(t, func(string, json.RawMessage) (string, []string) { return "{}", nil })
	registry := capCDP.NewRegistry(nil)
	browser, err := registry.Add(ctx, fake.srv.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	budget := link.NewAllocationBudget(3*capHTTP.MaxHead + link.MaxPayload)
	pair := publicationLinks(t, ctx, link.Options{AllocationBudget: budget, Hello: link.Hello{Caps: []string{"cdp"}}, Handlers: map[string]link.Handler{"cdp": registry.Handler()}}, link.Options{})
	s, err := pair.page.Open(ctx, map[string]any{"kind": "cdp", "browser": browser.ID}, link.OpenOptions{Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	command := bytes.Repeat([]byte{' '}, capHTTP.MaxHead)
	copy(command, []byte(`{"id":1,"sessionId":"unknown","method":"Runtime.evaluate","params":{"expression":"1"}}`))
	var size [4]byte
	be.BigEndian.PutUint32(size[:], uint32(len(command)))
	for i := 0; i < 3; i++ {
		if _, err = s.Write(size[:]); err != nil {
			break
		}
		if _, err = s.Write(command); err != nil {
			break
		}
	}
	_, readErr := io.Copy(io.Discard, s)
	var reset *link.ResetError
	if !errors.As(readErr, &reset) || reset.Code != "ENOBUFS" {
		t.Fatalf("parked exhaustion: %v (write %v)", readErr, err)
	}
	allocationZero(t, ctx, budget)
}
