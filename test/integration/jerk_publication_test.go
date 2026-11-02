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
	pair := publicationLinks(t, ctx, link.Options{
		Hello: link.Hello{Name: "host", Caps: []string{capjerk.Cap, capjerk.SealCap}}, Handlers: map[string]link.Handler{capjerk.Kind: s.Handle},
	}, link.Options{Hello: link.Hello{Name: "publication-test"}})
	select {
	case <-pair.page.Ready():
	case <-ctx.Done():
		t.Fatal("publication link did not become ready")
	}
	return pair.page
}

func publicationLinks(t *testing.T, ctx context.Context, nodeOptions, pageOptions link.Options) linkPair {
	t.Helper()
	api := link.NewAPI(link.APIOptions{Loopback: true})
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := link.New(pc, link.RolePage, pageOptions)
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
	n, err := link.Answer(ctx, api, signal.Message{T: signal.TypeOffer, Nonce: "publication", SDP: pc.LocalDescription().SDP}, send, nodeOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Close() })
	select {
	case <-n.Ready():
	case <-ctx.Done():
		t.Fatal("publication link did not become ready")
	}
	return linkPair{node: n, page: page}
}

type pausedHello struct {
	entered chan struct{}
	release chan struct{}
}

func (h pausedHello) MarshalJSON() ([]byte, error) {
	close(h.entered)
	<-h.release
	return []byte(`null`), nil
}

func TestJerkPendingNoticeWaitsForHello(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jerk")
	if err := exec.Command("go", "build", "-o", path, "./testdata/fakejerk").Run(); err != nil {
		t.Fatal("build fake jerk failed")
	}
	s := capjerk.New(capjerk.Options{Jerk: &jerk.Jerk{Path: path}, Seal: true})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	page := publicationPeer(t, ctx, s)
	st, err := page.Open(ctx, map[string]any{"kind": "jerk", "op": "seal", "provider": "minted"}, link.OpenOptions{})
	if err != nil {
		t.Fatal("seal open failed")
	}
	_ = st.CloseWrite()
	if _, err := io.Copy(io.Discard, st); err != nil {
		t.Fatal("seal failed")
	}
	leases := s.Leases()
	if len(leases) != 1 || s.Unseal(ctx, leases[0].LeaseID).Code != "" {
		t.Fatal("could not queue a revoke notice")
	}
	_ = page.Close()
	for _, mode := range []string{"jerk-revoke", "no-cap", "closed"} {
		t.Run(mode, func(t *testing.T) {
			capable := mode != "no-cap"
			pause := pausedHello{entered: make(chan struct{}), release: make(chan struct{})}
			var once sync.Once
			release := func() { once.Do(func() { close(pause.release) }) }
			defer release()
			notices := make(chan struct{}, 2)
			caps := []string{}
			if capable {
				caps = append(caps, capjerk.RevokeCap)
			}
			pair := publicationLinks(t, ctx, link.Options{Hello: link.Hello{Name: "host", Screen: pause}}, link.Options{
				Hello: link.Hello{Name: "page", Caps: caps},
				Handlers: map[string]link.Handler{capjerk.RevokeKind: func(st *link.Stream) {
					if err := st.Accept(nil); err != nil {
						return
					}
					notices <- struct{}{}
					_ = st.CloseWrite()
				}},
			})
			select {
			case <-pause.entered:
			case <-ctx.Done():
				t.Fatal("node HELLO was not paused")
			}
			attached := make(chan struct{})
			go func() { s.Attach(pair.node); close(attached) }()
			select {
			case <-pair.page.Done():
				t.Fatal("pending notice reached the page before node HELLO")
			case <-attached:
				t.Fatal("notice attachment completed before node HELLO")
			case <-time.After(250 * time.Millisecond):
			}
			if mode == "closed" {
				_ = pair.node.Close()
				select {
				case <-attached:
				case <-ctx.Done():
					t.Fatal("notice attachment did not stop on disconnect")
				}
				return
			}
			release()
			select {
			case <-pair.page.Ready():
			case <-ctx.Done():
				t.Fatal("page did not receive node HELLO")
			}
			select {
			case <-attached:
			case <-ctx.Done():
				t.Fatal("notice attachment did not finish")
			}
			if capable {
				select {
				case <-notices:
				case <-ctx.Done():
					t.Fatal("pending notice was not delivered")
				}
			} else {
				select {
				case <-notices:
					t.Fatal("notice sent without page capability")
				case <-time.After(250 * time.Millisecond):
				}
			}
			_ = pair.node.Close()
			_ = pair.page.Close()
		})
	}
}
