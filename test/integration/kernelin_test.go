package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/kernelin"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/pion/webrtc/v4"
)

type linkPair struct {
	node *link.Link
	page *link.Link
}

func kernelPage(tag string) link.Handler {
	return func(s *link.Stream) {
		switch tag {
		case "hang":
			<-s.Done()
			return
		case "die":
			_ = s.Link().Close()
			return
		}
		if err := s.Accept(nil); err != nil {
			return
		}
		var head []byte
		buf := make([]byte, 4096)
		for !bytes.Contains(head, []byte("\r\n\r\n")) {
			n, err := s.Read(buf)
			head = append(head, buf[:n]...)
			if err != nil {
				return
			}
		}
		_, _ = fmt.Fprintf(s, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(tag), tag)
		_ = s.CloseWrite()
		_, _ = io.Copy(io.Discard, s)
	}
}

func newLinkPair(t *testing.T, tag string, caps ...string) linkPair {
	t.Helper()
	api := link.NewAPI(link.APIOptions{Loopback: true})
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := link.New(pc, link.RolePage, link.Options{
		Hello:    link.Hello{Name: tag, Caps: caps},
		Handlers: map[string]link.Handler{kernelin.Kind: kernelPage(tag)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = page.Close() })
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gathered
	send := func(_ context.Context, m signal.Message) error {
		switch {
		case m.T == signal.TypeAnswer:
			return pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP})
		case m.T == signal.TypeCandidate && m.Candidate != nil:
			return pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: m.Candidate.Candidate, SDPMid: m.Candidate.SDPMid, SDPMLineIndex: m.Candidate.SDPMLineIndex})
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	msg := signal.Message{T: signal.TypeOffer, Nonce: "n-" + tag, SDP: pc.LocalDescription().SDP}
	node, err := link.Answer(ctx, api, msg, send, link.Options{Hello: link.Hello{Name: "node"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close() })
	select {
	case <-node.Ready():
	case <-time.After(20 * time.Second):
		t.Fatalf("link %s did not come up: %v", tag, node.Err())
	}
	return linkPair{node: node, page: page}
}

func newKernelService(t *testing.T, cfg kernelin.Config) *kernelin.Service {
	t.Helper()
	logs := &syncBuffer{}
	cfg.Status = func(format string, a ...any) { _, _ = fmt.Fprintf(logs, format+"\n", a...) }
	s := kernelin.New(cfg)
	t.Cleanup(func() {
		_ = s.Close()
		if t.Failed() {
			t.Logf("kernel service status:\n%s", logs.String())
		}
	})
	return s
}

func kernelGet(t *testing.T, s *kernelin.Service) (int, string) {
	t.Helper()
	addr := s.Addr()
	if addr == nil {
		t.Fatal("the kernel service is not listening")
	}
	c, err := net.DialTimeout("tcp", addr.String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: 3000.kernel.localhost\r\n\r\n")
	b, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	status := 0
	_, _ = fmt.Sscanf(string(b), "HTTP/1.1 %d", &status)
	_, body, _ := strings.Cut(string(b), "\r\n\r\n")
	return status, body
}

func waitLinkDown(t *testing.T, s *kernelin.Service, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.Links() != want {
		if time.Now().After(deadline) {
			t.Fatalf("%d links registered, want %d", s.Links(), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestKernelServiceNewestPageWins(t *testing.T) {
	s := newKernelService(t, kernelin.Config{})
	plain := newLinkPair(t, "plain")
	s.Add(plain.node)
	if s.Addr() != nil || s.Links() != 0 {
		t.Fatal("a page without kernel-in made the service listen")
	}
	a := newLinkPair(t, "first", kernelin.Cap)
	s.Add(a.node)
	b := newLinkPair(t, "second", kernelin.Cap)
	s.Add(b.node)
	c := newLinkPair(t, "plain-2")
	s.Add(c.node)
	if s.Links() != 2 {
		t.Fatalf("%d links registered", s.Links())
	}
	if status, body := kernelGet(t, s); status != 200 || body != "second" {
		t.Fatalf("got %d %q, want the newest page", status, body)
	}
	_ = b.page.Close()
	waitLinkDown(t, s, 1)
	if status, body := kernelGet(t, s); status != 200 || body != "first" {
		t.Fatalf("got %d %q after the newest page went", status, body)
	}
	d := newLinkPair(t, "third", kernelin.Cap)
	s.Add(d.node)
	if status, body := kernelGet(t, s); status != 200 || body != "third" {
		t.Fatalf("got %d %q, want the page that connected last", status, body)
	}
	_ = d.node.Close()
	_ = a.node.Close()
	waitLinkDown(t, s, 0)
	if status, body := kernelGet(t, s); status != 502 || body != "no seven page connected\n" {
		t.Fatalf("got %d %q with no page", status, body)
	}
}

func TestKernelServiceTimeouts(t *testing.T) {
	s := newKernelService(t, kernelin.Config{OpenTimeout: 300 * time.Millisecond, HeadTimeout: 500 * time.Millisecond})
	hang := newLinkPair(t, "hang", kernelin.Cap)
	s.Add(hang.node)
	start := time.Now()
	status, body := kernelGet(t, s)
	if status != http.StatusGatewayTimeout || body != "kernel port 3000 did not answer\n" || time.Since(start) > 5*time.Second {
		t.Fatalf("got %d %q after %v", status, body, time.Since(start))
	}
	c, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: 3000.kernel.localhost\r\n")
	start = time.Now()
	rest, err := io.ReadAll(c)
	if err != nil || len(rest) != 0 || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("slow head: %q %v after %v", rest, err, time.Since(start))
	}
}

func TestKernelServiceLinkDiesBeforeOpened(t *testing.T) {
	s := newKernelService(t, kernelin.Config{})
	die := newLinkPair(t, "die", kernelin.Cap)
	s.Add(die.node)
	if status, body := kernelGet(t, s); status != 502 || body != "no seven page connected\n" {
		t.Fatalf("got %d %q", status, body)
	}
}
