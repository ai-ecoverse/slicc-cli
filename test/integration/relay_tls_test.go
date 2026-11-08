package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/pion/transport/v5/stdnet"
	"github.com/pion/turn/v5"
	"github.com/pion/webrtc/v4"
)

func TestRelayOnlyTLSDataRoundTrip(t *testing.T) {
	if root := os.Getenv("SLICC_TEST_RELAY_ROOT"); root != "" {
		relayTLSDataRoundTrip(t, root, os.Getenv("SLICC_TEST_RELAY_URL"))
		return
	}
	fixture := httptest.NewTLSServer(nil)
	fixture.Close()
	listener, err := tls.Listen("tcp4", "127.0.0.1:0", &tls.Config{
		Certificates: fixture.TLS.Certificates,
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	server, err := turn.NewServer(turn.ServerConfig{
		Realm: "slicc-local-test",
		AuthHandler: func(attrs *turn.RequestAttributes) (string, []byte, bool) {
			return attrs.Username, turn.GenerateAuthKey("synthetic-user", "slicc-local-test", "synthetic-password"), attrs.Username == "synthetic-user"
		},
		ListenerConfigs: []turn.ListenerConfig{{
			Listener: listener,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{
				RelayAddress: net.ParseIP("127.0.0.1"),
				Address:      "127.0.0.1",
				Net:          &stdnet.Net{},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	root := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(root, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fixture.TLS.Certificates[0].Certificate[0]}), 0o600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestRelayOnlyTLSDataRoundTrip$", "-test.timeout=35s")
	cmd.Env = append(os.Environ(), "SLICC_TEST_RELAY_ROOT="+root,
		"SLICC_TEST_RELAY_URL=turns:"+listener.Addr().String()+"?transport=tcp",
		"GODEBUG="+os.Getenv("GODEBUG")+",x509usefallbackroots=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("local TLS relay subprocess: %v\n%s", err, out)
	}
}

func relayTLSDataRoundTrip(t *testing.T, root, url string) {
	t.Helper()
	cert, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cert) {
		t.Fatal("invalid local TLS certificate")
	}
	x509.SetFallbackRoots(pool)
	cfg := webrtc.Configuration{
		ICETransportPolicy: webrtc.ICETransportPolicyRelay,
		ICEServers: []webrtc.ICEServer{{URLs: []string{url},
			Username: "synthetic-user", Credential: "synthetic-password"}},
	}
	api := link.NewAPI(link.APIOptions{RelayOnly: true, Logf: t.Logf})
	a, err := api.NewPeerConnection(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := api.NewPeerConnection(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	got := make(chan string, 1)
	fail := make(chan error, 2)
	b.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnMessage(func(m webrtc.DataChannelMessage) {
			if err := dc.Send(m.Data); err != nil {
				fail <- err
			}
		})
	})
	dc, err := a.CreateDataChannel("local-relay-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	dc.OnOpen(func() {
		if err := dc.SendText("verified TLS relay round trip"); err != nil {
			fail <- err
		}
	})
	dc.OnMessage(func(m webrtc.DataChannelMessage) { got <- string(m.Data) })
	offer, err := a.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	relayGatherDescription(t, a, offer)
	if err := b.SetRemoteDescription(*a.LocalDescription()); err != nil {
		t.Fatal(err)
	}
	answer, err := b.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	relayGatherDescription(t, b, answer)
	if err := a.SetRemoteDescription(*b.LocalDescription()); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-got:
		if msg != "verified TLS relay round trip" {
			t.Fatalf("unexpected payload %q", msg)
		}
	case err := <-fail:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("no data-channel round trip")
	}
	for _, pc := range []*webrtc.PeerConnection{a, b} {
		pair, err := pc.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
		if err != nil || pair == nil {
			t.Fatalf("missing selected candidate pair: %v", err)
		}
		if pair.Local.Typ != webrtc.ICECandidateTypeRelay || pair.Remote.Typ != webrtc.ICECandidateTypeRelay {
			t.Fatalf("non-relay selected pair: %v", pair)
		}
	}
}

func relayGatherDescription(t *testing.T, pc *webrtc.PeerConnection, description webrtc.SessionDescription) {
	t.Helper()
	done := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(description); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("TLS relay gathering timed out")
	}
	count := 0
	for line := range strings.SplitSeq(pc.LocalDescription().SDP, "\r\n") {
		if strings.HasPrefix(line, "a=candidate:") {
			count++
			if !strings.Contains(line, " typ relay") || strings.Contains(line, ".local") {
				t.Fatalf("unexpected local candidate %q", line)
			}
		}
	}
	if count == 0 {
		t.Fatal("no TLS relay candidate allocated")
	}
}
