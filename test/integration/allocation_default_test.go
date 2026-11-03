package integration

import (
	"bytes"
	"context"
	be "encoding/binary"
	"encoding/json"
	capCDP "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"io"
	"testing"
	"time"
)

func TestSharedAllocationIdleRelaysAcrossLinks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	const large = 16 << 20
	fake := newFakeCDPBrowser(t, func(method string, _ json.RawMessage) (string, []string) {
		if method == "Browser.getVersion" {
			return `{"payload":"` + string(bytes.Repeat([]byte{'x'}, large)) + `"}`, nil
		}
		return "{}", nil
	})
	registry := capCDP.NewRegistry(nil)
	browser, err := registry.Add(ctx, fake.srv.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	options := link.Options{Hello: link.Hello{Caps: []string{"cdp"}}, Handlers: map[string]link.Handler{"cdp": registry.Handler()}}
	first := publicationLinks(t, ctx, options, link.Options{})
	second := publicationLinks(t, ctx, options, link.Options{})
	var streams []*link.Stream
	defer func() {
		for _, s := range streams {
			_ = s.Reset("ECONNRESET")
		}
	}()
	for i := 0; i < 32; i++ {
		page := first.page
		if i%2 != 0 {
			page = second.page
		}
		s, e := page.Open(ctx, map[string]any{"kind": "cdp", "browser": browser.ID}, link.OpenOptions{Shared: true})
		if e != nil {
			t.Fatalf("idle relay %d: %v", i+1, e)
		}
		streams = append(streams, s)
	}
	request := []byte(`{"id":1,"method":"Browser.getVersion"}`)
	var size [4]byte
	be.BigEndian.PutUint32(size[:], uint32(len(request)))
	if _, err = streams[0].Write(append(size[:], request...)); err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(streams[0], size[:]); err != nil {
		t.Fatal(err)
	}
	n := int64(be.BigEndian.Uint32(size[:]))
	if n < large {
		t.Fatalf("large browser reply: %d", n)
	}
	if _, err = io.CopyN(io.Discard, streams[0], n); err != nil {
		t.Fatal(err)
	}
	t.Logf("CDP idle concurrency regression: %d relays across two links; %d-byte browser reply", len(streams), n)
}

func TestSharedAllocationDefaultBackpressure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fake := newFakeCDPBrowser(t, func(string, json.RawMessage) (string, []string) { return "{}", nil })
	registry := capCDP.NewRegistry(nil)
	browser, err := registry.Add(ctx, fake.srv.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	pair := publicationLinks(t, ctx, link.Options{Hello: link.Hello{Caps: []string{"cdp"}}, Handlers: map[string]link.Handler{"cdp": registry.Handler()}}, link.Options{})
	var streams []*link.Stream
	defer func() {
		for _, s := range streams {
			_ = s.Reset("ECONNRESET")
		}
	}()
	chunk := bytes.Repeat([]byte{' '}, link.Window+1)
	var size [4]byte
	be.BigEndian.PutUint32(size[:], capCDP.MaxMessage)
	for i := 0; i < 8; i++ {
		s, e := pair.page.Open(ctx, map[string]any{"kind": "cdp", "browser": browser.ID}, link.OpenOptions{Shared: true})
		if e != nil {
			t.Fatal(e)
		}
		streams = append(streams, s)
		if _, e = s.Write(size[:]); e != nil {
			t.Fatal(e)
		}
		if i < 7 {
			if _, e = s.Write(chunk); e != nil {
				t.Fatal(e)
			}
		}
	}
	written := make(chan error, 1)
	go func() { _, e := streams[7].Write(chunk); written <- e }()
	select {
	case e := <-written:
		t.Fatalf("eighth 64 MiB parser exceeded shared reservation: %v", e)
	case <-time.After(100 * time.Millisecond):
	}
	_ = streams[0].Reset("ECONNRESET")
	select {
	case e := <-written:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("default parser did not resume after release")
	}
}
