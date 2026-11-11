package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/pion/ice/v4"
	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/stdnet"
	"github.com/pion/webrtc/v4"
)

func failedInterfaceDiscovery() *link.InterfaceDiscovery {
	return link.NewInterfaceDiscovery(func() ([]*transport.Interface, error) {
		return nil, errors.New("synthetic interface discovery failure")
	}, 100*time.Millisecond)
}

func syntheticInterfaces() []*transport.Interface {
	iface := transport.NewInterface(net.Interface{Index: 1, Name: "lo", Flags: net.FlagUp | net.FlagLoopback, MTU: 65536})
	iface.AddAddress(&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)})
	return []*transport.Interface{iface}
}

func syntheticPeerAPI() *webrtc.API {
	se := webrtc.SettingEngine{}
	se.SetNet(&stdnet.Net{})
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	return webrtc.NewAPI(webrtc.WithSettingEngine(se))
}

func TestInterfaceDiscoverySuccessAndRefresh(t *testing.T) {
	var calls atomic.Int32
	discovery := link.NewInterfaceDiscovery(func() ([]*transport.Interface, error) {
		iface := syntheticInterfaces()
		iface[0].Index = int(calls.Add(1))
		return iface, nil
	}, time.Second)
	for i := 1; i <= 2; i++ {
		if i > 1 {
			time.Sleep(1100 * time.Millisecond)
		}
		interfaces, err := discovery.Interfaces(context.Background())
		if err != nil || len(interfaces) != 1 || interfaces[0].Index != i {
			t.Fatalf("probe %d: interfaces=%v err=%v", i, interfaces, err)
		}
	}
}

func staticProbe(interfaces []*transport.Interface, err error) func() ([]*transport.Interface, error) {
	return func() ([]*transport.Interface, error) { return interfaces, err }
}

func TestInterfaceDiscoveryFailures(t *testing.T) {
	for name, probe := range map[string]func() ([]*transport.Interface, error){
		"error":         staticProbe(nil, errors.New("synthetic failure")),
		"panic":         func() ([]*transport.Interface, error) { panic("synthetic panic") },
		"empty":         staticProbe(nil, nil),
		"invalid first": staticProbe(append([]*transport.Interface{nil}, syntheticInterfaces()...), nil),
		"invalid last":  staticProbe(append(syntheticInterfaces(), nil), nil),
		"no addresses":  staticProbe([]*transport.Interface{transport.NewInterface(net.Interface{Index: 1, Name: "empty"})}, nil),
	} {
		t.Run(name, func(t *testing.T) {
			discovery := link.NewInterfaceDiscovery(probe, time.Second)
			for range 3 {
				if _, err := discovery.Interfaces(context.Background()); err == nil {
					t.Fatal("failed discovery reported success")
				} else if strings.Contains(err.Error(), "synthetic") {
					t.Fatalf("probe payload leaked into error: %v", err)
				}
				api := link.NewAPI(link.APIOptions{Discovery: discovery})
				assertAutomaticRelayConfiguration(t, api)
				pc, err := api.NewPeerConnection(webrtc.Configuration{})
				if pc != nil || err == nil || !strings.Contains(err.Error(), "requires a TURN-over-TLS TCP server") {
					t.Fatalf("expected actionable fallback failure: pc=%v err=%v", pc, err)
				}
			}
		})
	}
}

func TestInterfaceDiscoveryTimeoutAndBoundedRepeatedFailures(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	defer close(release)
	discovery := link.NewInterfaceDiscovery(func() ([]*transport.Interface, error) {
		calls.Add(1)
		<-release
		return syntheticInterfaces(), nil
	}, 20*time.Millisecond)
	start := time.Now()
	if _, err := discovery.Interfaces(context.Background()); err == nil {
		t.Fatal("hung probe reported success")
	}
	api := link.NewAPI(link.APIOptions{Discovery: discovery})
	assertAutomaticRelayConfiguration(t, api)
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: []webrtc.ICEServer{{URLs: []string{"turns:127.0.0.1:1?transport=tcp"}, Username: "synthetic-user", Credential: "synthetic-password"}}})
			if err != nil {
				t.Errorf("expected bounded automatic fallback: %v", err)
				return
			}
			defer func() { _ = pc.Close() }()
			if pc.GetConfiguration().ICETransportPolicy != webrtc.ICETransportPolicyRelay {
				t.Error("timed-out discovery with a TLS relay did not fall back to relay only")
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("hung discovery spawned %d probes", calls.Load())
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("hung discovery did not return promptly: %s", elapsed)
	}
}

func TestInterfaceDiscoveryTimeoutWithoutTLSRelayWaitsForLateResult(t *testing.T) {
	var calls atomic.Int32
	discovery := link.NewInterfaceDiscovery(func() ([]*transport.Interface, error) {
		calls.Add(1)
		time.Sleep(300 * time.Millisecond)
		return syntheticInterfaces(), nil
	}, 20*time.Millisecond)
	api := link.NewAPI(link.APIOptions{Discovery: discovery, Loopback: true})
	start := time.Now()
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:127.0.0.1:3478", "turn:127.0.0.1:3478?transport=udp"}, Username: "synthetic-user", Credential: "synthetic-password"}}})
	if err != nil {
		t.Fatalf("timed-out discovery without a TLS relay failed instead of waiting: %v", err)
	}
	defer func() { _ = pc.Close() }()
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Fatalf("peer connection did not wait for the late probe: %s", elapsed)
	}
	if cfg := pc.GetConfiguration(); cfg.ICETransportPolicy != webrtc.ICETransportPolicyAll || len(cfg.ICEServers) != 1 || len(cfg.ICEServers[0].URLs) != 2 {
		t.Fatalf("late successful discovery changed ICE configuration: %+v", cfg)
	}
	if calls.Load() != 1 {
		t.Fatalf("late success spawned %d probes", calls.Load())
	}
	assertDirectDataRoundTrip(t, api)
}

func TestInterfaceDiscoveryCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	discovery := link.NewInterfaceDiscovery(func() ([]*transport.Interface, error) {
		close(started)
		<-release
		return syntheticInterfaces(), nil
	}, 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := link.NewAPI(link.APIOptions{Discovery: discovery}).NewPeerConnectionContext(ctx, webrtc.Configuration{})
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("discovery did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled discovery hung")
	}
}

func TestAutomaticRelayPreservesSuccessfulConfiguration(t *testing.T) {
	discovery := link.NewInterfaceDiscovery(func() ([]*transport.Interface, error) { return syntheticInterfaces(), nil }, time.Second)
	api := link.NewAPI(link.APIOptions{Discovery: discovery, Loopback: true})
	cfg := webrtc.Configuration{ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:127.0.0.1:3478"}}}}
	pc, err := api.NewPeerConnection(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close() }()
	actual := pc.GetConfiguration()
	if actual.ICETransportPolicy != webrtc.ICETransportPolicyAll || !reflect.DeepEqual(actual.ICEServers, cfg.ICEServers) {
		t.Fatalf("successful discovery changed ICE configuration: %+v", actual)
	}
}

func TestAutomaticRelayHealthyDiscoveryGathersHostCandidate(t *testing.T) {
	discovery := link.NewInterfaceDiscovery(func() ([]*transport.Interface, error) { return syntheticInterfaces(), nil }, time.Second)
	pc, err := link.NewAPI(link.APIOptions{Discovery: discovery, Loopback: true}).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close() }()
	if _, err := pc.CreateDataChannel("normal-discovery", nil); err != nil {
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
		t.Fatal("normal host gathering timed out")
	}
	if !strings.Contains(pc.LocalDescription().SDP, " typ host") {
		t.Fatal("successful discovery did not preserve host candidates")
	}
}

func TestAutomaticRelayRejectsMissingTLSRelays(t *testing.T) {
	for _, server := range []webrtc.ICEServer{
		{},
		{URLs: []string{"stun:127.0.0.1:3478", "turn:127.0.0.1:3478?transport=tcp"}, Username: "test", Credential: "test"},
		{URLs: []string{"turns:127.0.0.1:5349?transport=udp"}, Username: "test", Credential: "test"},
		{URLs: []string{"turns:[fe80::1%25eth0]:5349?transport=tcp"}, Username: "test", Credential: "test"},
		{URLs: []string{"not a URL"}, Username: "test", Credential: "test"},
		{URLs: []string{"turns:127.0.0.1:5349?transport=tcp"}},
		{URLs: []string{"turns:127.0.0.1:5349?transport=tcp"}, Username: "test", Credential: 42},
	} {
		api := link.NewAPI(link.APIOptions{Discovery: failedInterfaceDiscovery()})
		_, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: []webrtc.ICEServer{server}})
		if err == nil || !strings.Contains(err.Error(), "requires a TURN-over-TLS TCP server") {
			t.Fatalf("URLs %v: %v", server.URLs, err)
		}
	}
}

func TestAutomaticRelayFiltersServersAndVerifiesTLS(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	url := "turns:" + strings.TrimPrefix(server.URL, "https://") + "?transport=tcp"
	peer, err := syntheticPeerAPI().NewPeerConnection(webrtc.Configuration{})
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
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := webrtc.GenerateCertificate(key)
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
	servers := []signal.ICEServer{
		{URLs: []string{"stun:127.0.0.1:3478", "turn:127.0.0.1:3478?transport=udp", url}, Username: "test", Credential: "test"},
		{URLs: []string{url}},
	}
	l, err := link.Answer(ctx, link.NewAPI(link.APIOptions{Discovery: failedInterfaceDiscovery(), Logf: logf}), signal.Message{SDP: offer.SDP, ICEServers: servers}, func(context.Context, signal.Message) error { return nil }, link.Options{Logf: logf, Certificates: []webrtc.Certificate{*certificate}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	pc := l.PeerConnection()
	cfg := pc.GetConfiguration()
	if cfg.ICETransportPolicy != webrtc.ICETransportPolicyRelay || len(cfg.ICEServers) != 1 || len(cfg.ICEServers[0].URLs) != 1 || cfg.ICEServers[0].URLs[0] != url {
		t.Fatalf("unexpected relay configuration: policy=%v servers=%d", cfg.ICETransportPolicy, len(cfg.ICEServers))
	}
	if len(cfg.Certificates) != 1 || !cfg.Certificates[0].Equals(*certificate) {
		t.Fatal("caller certificate was replaced")
	}
	if len(servers[0].URLs) != 3 || len(servers) != 2 {
		t.Fatal("caller ICE servers were mutated")
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

func TestAutomaticRelayPreservesSuppliedAPI(t *testing.T) {
	api := syntheticPeerAPI()
	peer, err := api.NewPeerConnection(webrtc.Configuration{})
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l, err := link.Answer(ctx, api, signal.Message{SDP: offer.SDP}, func(context.Context, signal.Message) error { return nil }, link.Options{})
	if err != nil {
		t.Fatalf("supplied API was replaced or re-probed: %v", err)
	}
	defer func() { _ = l.Close() }()
	if l.PeerConnection().GetConfiguration().ICETransportPolicy != webrtc.ICETransportPolicyAll {
		t.Fatal("supplied API configuration changed")
	}
}

func TestAutomaticRelayFollowHelp(t *testing.T) {
	out, errOut, code := slicc(t, "https://example.test/join/test", "follow", "--help")
	if code != 0 || strings.Contains(out, "--relay-only") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out, errOut)
	}
}

func assertAutomaticRelayConfiguration(t *testing.T, api *link.API) {
	t.Helper()
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:127.0.0.1:1", "turn:127.0.0.1:1?transport=udp", "turns:127.0.0.1:1?transport=tcp"}, Username: "synthetic-user", Credential: "synthetic-password"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close() }()
	cfg := pc.GetConfiguration()
	if cfg.ICETransportPolicy != webrtc.ICETransportPolicyRelay || len(cfg.ICEServers) != 1 || len(cfg.ICEServers[0].URLs) != 1 || cfg.ICEServers[0].URLs[0] != "turns:127.0.0.1:1?transport=tcp" {
		t.Fatalf("fallback configuration not TLS relay only: %+v", cfg)
	}
}

func TestInterfaceDiscoveryLateSuccess(t *testing.T) {
	release := make(chan struct{})
	finished := make(chan struct{})
	var calls atomic.Int32
	discovery := link.NewInterfaceDiscovery(func() ([]*transport.Interface, error) {
		calls.Add(1)
		<-release
		defer close(finished)
		return syntheticInterfaces(), nil
	}, 20*time.Millisecond)
	if _, err := discovery.Interfaces(context.Background()); err == nil {
		t.Fatal("hung probe unexpectedly succeeded")
	}
	close(release)
	<-finished
	deadline := time.Now().Add(time.Second)
	for {
		interfaces, err := discovery.Interfaces(context.Background())
		if err == nil && len(interfaces) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("late successful result was lost: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("late success spawned %d probes", calls.Load())
	}
}

func TestInterfaceDiscoverySlowRefreshKeepsLastGoodSnapshot(t *testing.T) {
	first := make(chan struct{})
	finished := make(chan struct{})
	hung := make(chan struct{})
	defer close(hung)
	var calls atomic.Int32
	discovery := link.NewInterfaceDiscovery(func() ([]*transport.Interface, error) {
		if calls.Add(1) == 1 {
			<-first
			defer close(finished)
			return syntheticInterfaces(), nil
		}
		<-hung
		return nil, errors.New("synthetic late probe")
	}, 20*time.Millisecond)
	if _, err := discovery.Interfaces(context.Background()); err == nil {
		t.Fatal("hung first probe unexpectedly succeeded")
	}
	close(first)
	<-finished
	time.Sleep(1100 * time.Millisecond)
	start := time.Now()
	interfaces, err := discovery.Interfaces(context.Background())
	if err != nil || len(interfaces) != 1 {
		t.Fatalf("slow refresh lost the last good snapshot: interfaces=%d err=%v", len(interfaces), err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("slow refresh was not bounded: %s", elapsed)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected one refresh probe, got %d probes", calls.Load())
	}
	api := link.NewAPI(link.APIOptions{Discovery: discovery, Loopback: true})
	cfg := webrtc.Configuration{ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:127.0.0.1:3478"}}}}
	pc, err := api.NewPeerConnection(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close() }()
	if actual := pc.GetConfiguration(); actual.ICETransportPolicy != webrtc.ICETransportPolicyAll || !reflect.DeepEqual(actual.ICEServers, cfg.ICEServers) {
		t.Fatalf("slow refresh after a good snapshot changed ICE configuration: %+v", actual)
	}
}

func TestAutomaticRelayRejectsMalformedOfferBeforeDiscovery(t *testing.T) {
	var calls atomic.Int32
	discovery := link.NewInterfaceDiscovery(func() ([]*transport.Interface, error) {
		calls.Add(1)
		return nil, errors.New("discovery should not run")
	}, time.Second)
	api := link.NewAPI(link.APIOptions{Discovery: discovery})
	offer := signal.Message{SDP: "not an SDP offer"}
	l, err := link.Answer(context.Background(), api, offer, nil, link.Options{})
	if l != nil || err == nil || !strings.Contains(err.Error(), "offer:") {
		t.Fatalf("expected malformed offer error: link=%v err=%v", l, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l, err = link.Answer(ctx, api, offer, nil, link.Options{})
	if l != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation before SDP validation: link=%v err=%v", l, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid or cancelled offer started %d probes", calls.Load())
	}
}

func TestAutomaticRelayFilteredInterfacesDoNotReprobeForMDNS(t *testing.T) {
	for _, test := range []struct {
		name  string
		flags net.Flags
		ip    net.IP
	}{
		{name: "down interface", flags: net.FlagLoopback, ip: net.ParseIP("127.0.0.1")},
		{name: "excluded loopback", flags: net.FlagUp | net.FlagLoopback, ip: net.ParseIP("127.0.0.1")},
		{name: "site local IPv6", flags: net.FlagUp, ip: net.ParseIP("fec0::1")},
		{name: "IPv4 compatible IPv6", flags: net.FlagUp, ip: net.ParseIP("::2")},
		{name: "invalid IP", flags: net.FlagUp, ip: net.IP{1, 2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			iface := transport.NewInterface(net.Interface{Index: 1, Name: "filtered", Flags: test.flags, MTU: 1500})
			iface.AddAddress(&net.IPAddr{IP: test.ip})
			discovery := link.NewInterfaceDiscovery(func() ([]*transport.Interface, error) { return []*transport.Interface{iface}, nil }, time.Second)
			pc, err := link.NewAPI(link.APIOptions{Discovery: discovery}).NewPeerConnection(webrtc.Configuration{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = pc.Close() }()
			if pc.GetConfiguration().ICETransportPolicy != webrtc.ICETransportPolicyAll {
				t.Fatal("successful filtered discovery changed ICE policy")
			}
			if _, err := pc.CreateDataChannel("filtered-discovery", nil); err != nil {
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
				t.Fatal("filtered interface gathering timed out")
			}
			if strings.Contains(pc.LocalDescription().SDP, "a=candidate:") {
				t.Fatal("filtered interface produced a candidate")
			}
		})
	}
}
