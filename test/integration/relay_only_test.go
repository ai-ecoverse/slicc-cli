package integration

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/pion/webrtc/v4"
)

func TestRelayOnlyInitializesWithoutInterfaces(t *testing.T) {
	api := link.NewAPI(link.APIOptions{RelayOnly: true})
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICETransportPolicy: webrtc.ICETransportPolicyRelay})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close() }()
	if _, err := pc.CreateDataChannel("control", nil); err != nil {
		t.Fatal(err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	done := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("gathering did not finish")
	}
	if strings.Contains(pc.LocalDescription().SDP, "a=candidate:") {
		t.Fatal("relay-only without a relay gathered a candidate")
	}
}

func TestRelayOnlyRejectsMissingTLSRelays(t *testing.T) {
	for _, urls := range [][]string{
		nil,
		{"stun:127.0.0.1:3478", "turn:127.0.0.1:3478?transport=tcp"},
		{"turns:127.0.0.1:5349?transport=udp"},
		{"turns:[fe80::1%25eth0]:5349?transport=tcp"},
		{"not a URL"},
	} {
		offer := signal.Message{ICEServers: []signal.ICEServer{{URLs: urls, Username: "test", Credential: "test"}}}
		_, err := link.Answer(context.Background(), nil, offer, nil, link.Options{RelayOnly: true})
		if err == nil || !strings.Contains(err.Error(), "requires a TURN-over-TLS TCP server") {
			t.Fatalf("URLs %v: %v", urls, err)
		}
	}
}

func TestRelayOnlyFiltersServersAndVerifiesTLS(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	url := "turns:" + strings.TrimPrefix(server.URL, "https://") + "?transport=tcp"
	api := link.NewAPI(link.APIOptions{RelayOnly: true})
	peer, err := api.NewPeerConnection(webrtc.Configuration{ICETransportPolicy: webrtc.ICETransportPolicyRelay})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	if _, err := peer.CreateDataChannel("control", nil); err != nil {
		t.Fatal(err)
	}
	offer, err := peer.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var messages []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		messages = append(messages, fmt.Sprintf(format, args...))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l, err := link.Answer(ctx, link.NewAPI(link.APIOptions{RelayOnly: true, Logf: logf}), signal.Message{SDP: offer.SDP, ICEServers: []signal.ICEServer{
		{URLs: []string{"stun:127.0.0.1:3478", "turn:127.0.0.1:3478?transport=udp", url}, Username: "test", Credential: "test"},
		{URLs: []string{url}},
	}}, func(context.Context, signal.Message) error { return nil }, link.Options{RelayOnly: true, Logf: logf})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	pc := l.PeerConnection()
	cfg := pc.GetConfiguration()
	if cfg.ICETransportPolicy != webrtc.ICETransportPolicyRelay || len(cfg.ICEServers) != 1 || len(cfg.ICEServers[0].URLs) != 1 || cfg.ICEServers[0].URLs[0] != url {
		t.Fatalf("unexpected relay configuration: policy=%v servers=%d", cfg.ICETransportPolicy, len(cfg.ICEServers))
	}
	select {
	case <-webrtc.GatheringCompletePromise(pc):
	case <-time.After(10 * time.Second):
		t.Fatal("gathering did not finish")
	}
	if strings.Contains(pc.LocalDescription().SDP, "a=candidate:") {
		t.Fatal("untrusted TLS relay produced a candidate")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(messages, "\n"), "certificate") {
		t.Fatalf("expected certificate verification failure: %v", messages)
	}
}

func TestRelayOnlyFollowHelp(t *testing.T) {
	out, errOut, code := slicc(t, "https://example.test/join/test", "follow", "--help")
	if code != 0 || !strings.Contains(out, "--relay-only") || !strings.Contains(out, "TLS/TCP") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out, errOut)
	}
}

func TestRelayOnlyFollowRejectsOfferWithoutTLSRelay(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("relay-only")
	h.with(func() { tr.IceServers = nil })
	f := startFlowBinary(t, binary, configEnv(t), h.joinURL(tr), "follow", "--relay-only", "--json")
	f.next(t, "connecting")
	e := f.next(t, "error")
	if !strings.Contains(e.raw, "requires a TURN-over-TLS TCP server") {
		t.Fatalf("unexpected error: %s", e.raw)
	}
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	if code := f.rest(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
}
