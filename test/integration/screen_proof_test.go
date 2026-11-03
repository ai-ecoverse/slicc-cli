//go:build !windows

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	nativeScreen "github.com/ai-ecoverse/slicc-cli/internal/caps/screen"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

func TestScreenProofKeepsInjectedVerifierRefusals(t *testing.T) {
	for _, lane := range []string{"s", "f"} {
		for _, late := range []bool{false, true} {
			for _, enabled := range []bool{false, true} {
				name := lane
				if late {
					name += "-late"
				} else {
					name += "-refused"
				}
				if enabled {
					name += "-observed"
				} else {
					name += "-ordinary"
				}
				t.Run(name, func(t *testing.T) {
					var armed atomic.Bool
					release := make(chan struct{})
					defer close(release)
					verifier := func(conn *net.UnixConn, _ nativeScreen.Peer) error {
						if armed.Load() && strings.HasSuffix(conn.RemoteAddr().String(), "/"+lane) {
							if late {
								<-release
								return nil
							}
							return errors.New("PRIVATE_NATIVE_FAILURE_SENTINEL")
						}
						return nil
					}
					var observer *nativeScreen.ProofObserver
					if enabled {
						observer = nativeScreen.NewProofObserver()
					}
					c, a := newScreenAppDialing(t, true, verifier, func(ctx context.Context, b nativeScreen.Bootstrap, verify nativeScreen.Verifier, audit nativeScreen.Audit) (*nativeScreen.Client, error) {
						observed := func(conn *net.UnixConn, peer nativeScreen.Peer) (nativeScreen.ProofChecks, error) {
							e := verify(conn, peer)
							matched := e == nil
							return nativeScreen.ProofChecks{TokenMatched: &matched, PIDAgreed: &matched, UIDAgreed: &matched}, e
						}
						return nativeScreen.ConnectWithProof(ctx, b, observed, audit, observer)
					})
					b := c.Broker()
					page, states := screenLinks(t, b)
					screenState(t, states, false)
					binding := screenBinding(b)
					a.send(t, map[string]any{"type": "grant", "request": nativeScreen.ID(16), "binding": binding, "scope": "window", "limits": nativeScreen.DefaultLimits()})
					screenState(t, states, true)
					screenNativeMessage(t, a, "granted")
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					stream, e := page.Open(ctx, nativeScreen.Open{Kind: "screen", V: 1, Binding: binding}, link.OpenOptions{})
					if e != nil {
						t.Fatal(e)
					}
					_ = stream.CloseWrite()
					armed.Store(true)
					if lane == "f" {
						frame := nativeScreen.Frame{Type: "frame", V: 1, Binding: binding, Seq: "1", Captured: "1", Width: 2, Height: 2, Codec: "jpeg", Launch: a.launch, Connection: a.connection}
						if e = nativeScreen.WriteRecord(a.frames, frame, screenJPEG(t), time.Second); e != nil {
							t.Fatal(e)
						}
					}
					state := screenState(t, states, false)
					if state.Reason != "peer-lost" || c.Authenticated() || len(b.Snapshot().Caps) != 0 {
						t.Fatal("observer changed refusal/invalidation", enabled, late, lane, state.Reason)
					}
					select {
					case <-stream.Done():
					case <-ctx.Done():
						t.Fatal("refusal retained screen stream")
					}
					var reset *link.ResetError
					if !errors.As(stream.Err(), &reset) || reset.Code != "EACCES" {
						t.Fatal("refusal changed stream result", stream.Err())
					}
					if enabled {
						count := 0
						complete := false
						for !complete {
							select {
							case report, ok := <-observer.Reports():
								if !ok {
									t.Fatal("missing observer completion")
								}
								count++
								if report.Seq != count || count > 5 {
									t.Fatal("unbounded/gapped observations")
								}
								raw, e := json.Marshal(report)
								if e != nil || strings.Contains(string(raw), "PRIVATE_NATIVE_FAILURE_SENTINEL") || len(raw) > 512 {
									t.Fatal("content leaked or observation oversized")
								}
								if report.Outcome == "refused" && (c.Authenticated() || len(b.Snapshot().Caps) != 0) {
									t.Fatal("observation preceded stop")
								}
								complete = report.Stage == "complete"
							case <-ctx.Done():
								t.Fatal("observer waited for native failure or stdout")
							}
						}
					}
				})
			}
		}
	}
}
func TestScreenProofFlagRequiresNativeAndJSON(t *testing.T) {
	out, _, code := slicc(t, "capabilities", "--json")
	if code != 0 || !strings.Contains(out, `"native-screen-verification"`) {
		t.Fatal("diagnostic event missing from discovery")
	}

	for _, args := range [][]string{{"--native-screen-proof"}, {"--native-screen-proof", "--json"}, {"--native-screen-proof", "--native-screen"}} {
		_, stderr, code := slicc(t, append([]string{"http://127.0.0.1:1/join/synthetic.0000", "follow"}, args...)...)
		if code != 2 || !strings.Contains(stderr, "--native-screen-proof requires --native-screen and --json") {
			t.Fatal("diagnostic did not fail before setup", code, stderr)
		}
	}
}

func TestScreenProofCannotTrustAnEmptyPeer(t *testing.T) {
	if nativeScreen.VerifyApp(&net.UnixConn{}, nativeScreen.Peer{}) == nil {
		t.Fatal("empty peer trusted without observations")
	}
	checks, e := nativeScreen.VerifyAppWithProof(&net.UnixConn{}, nativeScreen.Peer{})
	if e == nil || checks.TokenMatched != nil || checks.PIDAgreed != nil || checks.UIDAgreed != nil {
		t.Fatal("observer created trust or invented kernel evidence")
	}
}
