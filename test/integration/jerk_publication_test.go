package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	capjerk "github.com/ai-ecoverse/slicc-cli/internal/caps/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/pion/webrtc/v4"
)

func TestJerkRecordsLeaseBeforePublication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jerk")
	build := exec.Command("go", "build", "-o", path, "./testdata/fakejerk")
	if err := build.Run(); err != nil {
		t.Fatal("build fake jerk failed")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	s := capjerk.New(capjerk.Options{Jerk: &jerk.Jerk{Path: path}, Seal: true, Notify: func(c node.Credential) {
		if c.Op == capjerk.OpSeal {
			close(entered)
			<-release
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	page := publicationPeer(t, ctx, s)
	defer unblock()
	st, err := page.Open(ctx, map[string]any{"kind": "jerk", "op": "seal", "provider": "minted"}, link.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.CloseWrite()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("seal was not recorded")
	}
	leases := s.Leases()
	if len(leases) != 1 || leases[0].Provider != "minted" || leases[0].ExpiresAt == "" {
		t.Fatal("lease metadata missing before publication")
	}
	_ = st.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	buf := make([]byte, 1)
	if n, err := st.Read(buf); n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("lease event reached the peer before recording completed")
	}
	_ = st.SetReadDeadline(time.Now().Add(10 * time.Second))
	unblock()
	dec := json.NewDecoder(st)
	var lease struct {
		Event string `json:"event"`
		ID    string `json:"lease_id"`
	}
	if err := dec.Decode(&lease); err != nil || lease.Event != "lease" || lease.ID != leases[0].LeaseID {
		t.Fatal("peer did not receive the recorded lease")
	}
	if out := s.Unseal(ctx, lease.ID); out.Code != "" || out.Upstream == nil || !*out.Upstream {
		t.Fatal("host could not revoke the published lease")
	}
	if len(s.Leases()) != 0 {
		t.Fatal("revoked lease remained listed")
	}
	_, _ = io.Copy(io.Discard, st)
}

func publicationPeer(t *testing.T, ctx context.Context, s *capjerk.Server) *link.Link {
	t.Helper()
	api := link.NewAPI(link.APIOptions{Loopback: true})
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := link.New(pc, link.RolePage, link.Options{Hello: link.Hello{Name: "publication-test"}})
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
	select {
	case <-gathered:
	case <-ctx.Done():
		t.Fatal("ICE gathering timed out")
	}
	send := func(_ context.Context, m signal.Message) error {
		if m.T == signal.TypeAnswer {
			return pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP})
		}
		if m.T == signal.TypeCandidate && m.Candidate != nil {
			return pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: m.Candidate.Candidate, SDPMid: m.Candidate.SDPMid, SDPMLineIndex: m.Candidate.SDPMLineIndex})
		}
		return nil
	}
	n, err := link.Answer(ctx, api, signal.Message{T: signal.TypeOffer, Nonce: "publication", SDP: pc.LocalDescription().SDP}, send, link.Options{
		Hello: link.Hello{Name: "host", Caps: []string{capjerk.Cap, capjerk.SealCap}}, Handlers: map[string]link.Handler{capjerk.Kind: s.Handle},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Close() })
	select {
	case <-page.Ready():
	case <-ctx.Done():
		t.Fatal("publication link did not become ready")
	}
	return page
}
