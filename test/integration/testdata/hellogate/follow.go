package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/pion/webrtc/v4"
)

func helloGateFollowCert(t *testing.T) webrtc.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := webrtc.GenerateCertificate(key)
	if err != nil {
		t.Fatal(err)
	}
	return *cert
}

func helloGateFollowPin(t *testing.T, cert webrtc.Certificate) string {
	t.Helper()
	fps, err := cert.GetFingerprints()
	if err != nil || len(fps) == 0 {
		t.Fatal(err)
	}
	return strings.ToLower(strings.ReplaceAll(fps[0].Value, ":", ""))
}

func helloGateFollowTranscript(t *testing.T, text, pin string) {
	t.Helper()
	for _, bad := range []string{"BEGIN CERTIFICATE", "END CERTIFICATE", "/join/", "lease-pins"} {
		if strings.Contains(text, bad) {
			t.Fatalf("transcript names %q", bad)
		}
	}
	if pin != "" && strings.Contains(text, pin) {
		t.Fatal("transcript names the paired fingerprint")
	}
	other := helloGateFollowPin(t, helloGateFollowCert(t))
	if strings.Contains(text, other) {
		t.Fatal("transcript names another certificate fingerprint")
	}
	if strings.Contains(text, "-----") {
		t.Fatal("transcript looks like PEM")
	}
	if strings.Contains(text, "/Users/") || strings.Contains(text, "/tmp/") {
		t.Fatal("transcript names an absolute path")
	}
}

type helloGateFailSub struct {
	mu   sync.Mutex
	msgs []string
}

func (s *helloGateFailSub) note(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, leaseLinkFailMessage(err))
}

func (s *helloGateFailSub) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.msgs, "\n")
}

func TestP3RefusalText(t *testing.T) {
	sub := &helloGateFailSub{}
	sub.note(link.ErrPeerRefused)
	sub.note(link.ErrHelloTimeout)
	msg := sub.msgs[0]
	other := sub.msgs[1]
	if msg != "paired fingerprint mismatch" {
		t.Fatalf("refusal text %q", msg)
	}
	if other != "lease peer did not establish an exclusive link" {
		t.Fatalf("other Done text %q", other)
	}
	helloGateFollowTranscript(t, sub.text(), "")
}

func TestP3NodeForwardsPeerGate(t *testing.T) {
	pin := helloGateFollowPin(t, helloGateFollowCert(t))
	cfg := node.Config{
		Name: "work", Host: "work", Version: "test", Mode: "remote",
		Certificates: []webrtc.Certificate{helloGateFollowCert(t)},
		PeerGate:     func(pc *webrtc.PeerConnection) bool { return pinMatches(pc, pin) },
	}
	n := node.New(cfg)
	opts := n.Options()
	if opts.PeerGate == nil {
		t.Fatal("node.Options dropped PeerGate")
	}
	helloGateFollowTranscript(t, "", pin)
}

func TestP3EmptyLeafFailClosed(t *testing.T) {
	sum := sha256.Sum256(nil)
	emptyPin := hex.EncodeToString(sum[:])
	if pinMatches(nil, emptyPin) {
		t.Fatal("nil pc accepted")
	}
	api := link.NewAPI(link.APIOptions{Loopback: true})
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	if pinMatches(pc, emptyPin) {
		t.Fatal("empty leaf accepted against the empty digest pin")
	}
	helloGateFollowTranscript(t, "", emptyPin)
}

func TestP3ErrorMappingUsesPeerRefused(t *testing.T) {
	sub := &helloGateFailSub{}
	sub.note(link.ErrPeerRefused)
	if sub.msgs[0] != "paired fingerprint mismatch" {
		t.Fatal(sub.msgs[0])
	}
	if !errors.Is(link.ErrPeerRefused, link.ErrPeerRefused) {
		t.Fatal("errors.Is lost ErrPeerRefused")
	}
	helloGateFollowTranscript(t, sub.text(), "")
}
