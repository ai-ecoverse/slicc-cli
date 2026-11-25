//go:build !windows

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/lease"
)

func TestSignedLeaseFirstPairReview(t *testing.T) {
	for _, tc := range []struct {
		name, decision string
		grant          bool
	}{{"EnterCancels", "", false}, {"ApproveMatchingHolder", "APPROVE", true}} {
		t.Run(tc.name, func(t *testing.T) {
			owner := launchBrowser(t)
			_, page := launchWithPage(t, pageServer(t).URL+"/index.html?lease")
			hub := newFakeHub(t)
			tray := hub.addTray("first")
			newTrayLeader(t, hub, page, tray)
			dir, err := os.MkdirTemp("/tmp", "sl-boot-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			t.Setenv("TMPDIR", dir)
			t.Setenv("XDG_RUNTIME_DIR", dir)
			env := configEnv(t)
			entries := func() []ctl.Entry {
				t.Helper()
				rows, err := ctl.ListLocal(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				return rows
			}
			if len(entries()) != 0 {
				t.Fatal("expected a fresh owner control directory")
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				rows, err := ctl.ListLocal(ctx)
				if err != nil {
					t.Error(err)
				}
				for _, row := range rows {
					if _, err := ctl.Send(ctx, row.Path, ctl.OpStop); err != nil {
						t.Errorf("stop detached fixture broker: %v", err)
					}
				}
			})

			var pin, jkt string
			page.eval("leaseFingerprint()", &pin)
			page.eval("leaseTest.init()", &jkt)
			const origin = "https://billing.example"
			p := startTTY(t, env, "lease", "review", "--to", "jkt:"+jkt, "--allow", origin, "--for", "5m")
			p.wait(t, 0, "literal loopback HTTP origin:")
			p.keys(t, owner.endpoint+"\n")
			p.wait(t, 0, "Local link label:")
			p.keys(t, "first-pair\n")
			p.wait(t, 0, "fingerprint from your trusted task conversation")
			p.keys(t, pin+"\n")
			p.wait(t, 0, "Paste its join URL here (hidden):")
			p.keys(t, hub.joinURL(tray)+"\n")
			screen := p.wait(t, 0, "type REVIEW:")
			rows := entries()
			if len(rows) != 1 || rows[0].Status == nil {
				t.Fatalf("expected one live detached broker, got %d sockets", len(rows))
			}
			row := rows[0]
			if row.PID == p.cmd.Process.Pid || row.Status.PID != row.PID || row.Status.Mode != "follow" || !slices.Equal(row.Status.Offers, []string{"cdp-lease-v1"}) || len(row.Status.Device) != 64 || !strings.Contains(screen, row.Status.Device) || strings.Contains(screen, tray.Token) {
				t.Fatal("review did not discover the detached lease-only broker, show its fingerprint and hide the join token")
			}
			page.waitUp(t, "")
			page.eval("otherHolder.init()", nil)
			pending := func(holder string) string {
				t.Helper()
				var r struct{ Type, Request string }
				page.eval(holder+`.request([{kind:'origin',url:'https://billing.example'}],{ttlSeconds:300})`, &r)
				if r.Type != "pending" || r.Request == "" {
					t.Fatal("holder did not establish a proven pending request")
				}
				return r.Request
			}
			other, wanted := pending("otherHolder"), pending("leaseTest")
			p.keys(t, "REVIEW\n")
			screen = p.wait(t, 0, "Type APPROVE to issue")
			if !strings.Contains(screen, "Link first-pair · Browser "+owner.endpoint+" · one new tab · holder jkt:"+jkt) || !strings.Contains(screen, "origin: "+origin) {
				t.Fatal("approval did not display the selected link, browser, holder and scope")
			}
			p.keys(t, tc.decision+"\n")
			select {
			case err := <-p.done:
				var exit *exec.ExitError
				if (tc.grant && err != nil) || (!tc.grant && (!errors.As(err, &exit) || exit.ExitCode() != 1)) {
					t.Fatalf("review decision %q: %v", tc.decision, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("review did not exit after the decision")
			}
			var list lease.Snapshot
			if err := json.Unmarshal([]byte(rawCtl(t, row.Path, ctl.OpLeaseList)), &list); err != nil {
				t.Fatal(err)
			}
			hasPending := func(id string) bool {
				return slices.ContainsFunc(list.Pending, func(r lease.Pending) bool { return r.Request == id })
			}
			if !hasPending(other) {
				t.Fatal("decision consumed a different holder's request")
			}
			if !tc.grant {
				if len(list.Pending) != 2 || !hasPending(wanted) || len(list.Active) != 0 {
					t.Fatal("Enter must leave both requests pending and no lease active")
				}
				return
			}
			var leaseID, state string
			page.eval("leaseTest.grant()", &leaseID)
			page.eval("leaseTest.connect()", &state)
			if state != "ready" || len(list.Pending) != 1 || len(list.Active) != 1 || list.Active[0].Lease != leaseID || list.Active[0].JKT != jkt || strings.Contains(p.text(), tray.Token) {
				t.Fatal("approved job did not receive its lease on the detached broker")
			}
		})
	}
}
