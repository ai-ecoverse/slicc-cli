package lease

import (
	"encoding/json"
	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	guarded "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func (tr *cleanupTrace) leaseTarget(t *testing.T) (string, string) {
	t.Helper()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	var target, session string
	for _, m := range tr.commands {
		if m.Method != "Target.createTarget" && m.Method != "Target.attachToTarget" {
			continue
		}
		for _, reply := range tr.replies {
			if reply.ID != m.ID {
				continue
			}
			var ids struct{ TargetID, SessionID string }
			if json.Unmarshal(reply.Result, &ids) == nil {
				if m.Method == "Target.createTarget" {
					target = ids.TargetID
				} else {
					session = ids.SessionID
				}
			}
		}
	}
	if target == "" || session == "" {
		t.Fatalf("live fixture had no owned target/session: %q %q", target, session)
	}
	return target, session
}

func (tr *cleanupTrace) evidence(session string) (bool, bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	frameReads := map[int64]bool{}
	for _, cmd := range tr.commands {
		if cmd.Method == "Page.getFrameTree" && cmd.Session == session {
			frameReads[cmd.ID] = true
		}
	}
	var errorPage, detach bool
	replies := tr.replies
	if tr.firstClose {
		replies = replies[:tr.repliesBeforeClose]
	}
	for _, reply := range replies {
		if frameReads[reply.ID] {
			var p struct{ FrameTree struct{ Frame frame } }
			if json.Unmarshal(reply.Result, &p) == nil && p.FrameTree.Frame.UnreachableURL != "" {
				errorPage = true
			}
		}
		if reply.Method == "Target.detachedFromTarget" && reply.Session == "" {
			var p struct{ SessionID string }
			if json.Unmarshal(reply.Params, &p) == nil && p.SessionID == session {
				detach = true
			}
		}
	}
	return errorPage, detach
}

func TestLeaseCleanupLive(t *testing.T) {
	mode, endpoint, site, protected := os.Getenv("SLICC_CLEANUP_LIVE"), os.Getenv("SLICC_CLEANUP_ENDPOINT"), os.Getenv("SLICC_CLEANUP_SITE"), os.Getenv("SLICC_CLEANUP_PROTECTED")
	if endpoint == "" || site == "" || protected == "" {
		t.Fatal("live cleanup fixture environment missing")
	}
	var baseline cleanupResult
	run := func(t *testing.T, child, withoutCleanup bool) {
		tr := &cleanupTrace{}
		t.Cleanup(guarded.LeaseCleanupTrace(tr.record))
		o, err := protect.New([]string{protected})
		if err != nil {
			t.Fatal(err)
		}
		bs := &browserSession{tabs: map[string]*tab{}}
		bs.c, err = guarded.OpenLocal(t.Context(), endpoint, o.Extra(), func(ev cdp.Event) {
			if !withoutCleanup || ev.Method != "Target.detachedFromTarget" {
				bs.observe(ev)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		f := newCleanupBroker(t, o, bs, nil)
		f.a.Allow = []Rule{{Kind: "origin", URL: site}}
		if !ValidRules(f.a.Allow, o) {
			t.Fatal("invalid live policy")
		}
		cleanupPhase(t)
		if child {
			if r := f.action(t, "goto", site+"/child"); r.err != nil && r.err != denied {
				t.Fatalf("child fixture goto: %v", r.err)
			}
			deadline := time.NewTimer(6 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			for {
				r := f.action(t, "snapshot", "")
				if r.err == denied {
					cleanupRefusal(t, r, denied)
					if withoutCleanup {
						baseline = r
					} else if r.err != baseline.err || !slices.Equal(r.payload, baseline.payload) {
						t.Fatalf("holder refusal changed: baseline=%+v observed=%+v", baseline, r)
					}
					break
				}
				if r.err != nil {
					t.Fatalf("child holder got %v", r.err)
				}
				select {
				case <-tick.C:
				case <-deadline.C:
					t.Fatal("protected child never refused holder")
				}
			}
			target, session := tr.leaseTarget(t)
			_, detach := tr.evidence(session)
			if detach || len(tr.closes()) != 0 {
				t.Fatalf("child frame detached lease=%t or issued parent close %v", detach, tr.closes())
			}
			targets, err := bs.c.Targets(t.Context())
			if err != nil || !slices.ContainsFunc(targets, func(info cdp.TargetInfo) bool { return info.TargetID == target }) {
				t.Fatalf("parent target missing after child refusal: %v %v", targets, err)
			}
			return
		}
		cleanupRefusal(t, f.action(t, "goto", site+"/redirect"), denied)
		f.b.bs.cleanup.Wait()
		target, session := tr.leaseTarget(t)
		errorPage, detachedBeforeClose := tr.evidence(session)
		attempts := tr.closes()
		if !errorPage || detachedBeforeClose || len(attempts) != 1 || string(attempts[0].Params) != literal(map[string]string{"targetId": target}) {
			t.Fatalf("error-page evidence=%t detach before attempt=%t guarded close attempts=%+v", errorPage, detachedBeforeClose, attempts)
		}
		if reply := tr.response(attempts[0].ID); reply.Error != nil || !strings.Contains(string(reply.Result), `"success":true`) {
			t.Fatalf("guarded close reply: %+v", reply)
		}
		cleanupRefusal(t, f.action(t, "snapshot", ""), denied)
		f.revoke(t)
		if len(tr.closes()) != 1 {
			t.Fatalf("duplicate close after revoke: %+v", tr.closes())
		}
	}
	switch mode {
	case "redirect":
		t.Run("R15aRedirectToProtectedHostViaErrorPageNotDetach", func(t *testing.T) { run(t, false, false) })
	case "child":
		t.Run("R15bProtectedChildFrameDoesNotCloseTheParent", func(t *testing.T) {
			t.Run("Baseline", func(t *testing.T) { run(t, true, true) })
			t.Run("Cleanup", func(t *testing.T) { run(t, true, false) })
		})
	default:
		t.Fatalf("unknown live cleanup leg %q", mode)
	}
}
