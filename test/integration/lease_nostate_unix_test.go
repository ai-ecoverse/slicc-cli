//go:build !windows

package integration

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

func startLeaseFollower(t *testing.T, fx *coldFixture, joinURL string) *exec.Cmd {
	t.Helper()
	pair := exec.Command(binary, "lease", "pair", "--link", "nostate", "--peer", fx.seven)
	pair.Env = fx.env
	if out, err := pair.CombinedOutput(); err != nil {
		t.Fatalf("pair: %v %s", err, out)
	}
	cmd := exec.Command(binary, "-", "follow", "--lease-browser", fx.endpoint, "--name", "nostate")
	cmd.Env = fx.env
	cmd.Stdin = strings.NewReader(joinURL + "\n")
	log := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Log(log.String())
		}
	})
	return cmd
}

func waitAttach(t *testing.T, h *fakeHub, token string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); len(attachRequests(h, token)) == 0; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the lease-only follower never attached to the typed join URL")
		}
	}
}

func waitOrdinaryJoinSaved(t *testing.T, plain, join, token string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		data, err := os.ReadFile(plain)
		if err == nil && strings.Contains(string(data), token) && tray.OpenState(plain).Current(join) == join {
			return
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("ordinary join state read: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("ordinary follow never saved join state: %v %s", err, data)
		}
	}
}

func joinStatePaths(t *testing.T) (string, string) {
	t.Helper()
	path, err := tray.DefaultStatePath()
	if err != nil {
		t.Fatal(err)
	}
	return path, path + ".lease"
}

func mustAbsent(t *testing.T, path, label string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s exists", label)
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s stat: %v", label, err)
	}
}

func TestSignedLeaseFollowerKeepsNoJoinState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seeded bool
	}{{"IgnoresAForeignSuccessor", true}, {"IgnoresASameOriginEntry", true}, {"SupersedesInMemory", false}} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newColdFixture(t, false)
			fx.endpoint = launchBrowser(t).endpoint
			plain, leased := joinStatePaths(t)
			typed := fx.hub.joinURL(fx.tray)
			want := fx.tray.Token
			foreign, foreignConns := countedHub(t)
			stored := fx.hub.addTray("stored")
			switch tc.name {
			case "IgnoresAForeignSuccessor":
				if err := tray.OpenState(leased).Save(typed, foreign.joinURL(foreign.addTray("foreign"))); err != nil {
					t.Fatal(err)
				}
			case "IgnoresASameOriginEntry":
				if err := tray.OpenState(leased).Save(typed, fx.hub.joinURL(stored)); err != nil {
					t.Fatal(err)
				}
			default:
				chain := supersedeChain(fx.hub, 1)
				typed, want = fx.hub.joinURL(chain[0]), chain[1].Token
			}
			before, _ := os.ReadFile(leased)
			info, _ := os.Stat(leased)
			startLeaseFollower(t, fx, typed)
			waitAttach(t, fx.hub, want)
			for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
				after, err := os.ReadFile(leased)
				if tc.seeded {
					now, errStat := os.Stat(leased)
					if err != nil || errStat != nil {
						t.Fatalf("lease join state: read=%v stat=%v", err, errStat)
					}
					if !bytes.Equal(before, after) || !now.ModTime().Equal(info.ModTime()) {
						t.Fatal("the lease-only follower changed the old lease join state")
					}
					break
				}
				if err == nil {
					t.Fatal("the lease-only follower wrote lease join state")
				}
				if !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("lease join state stat: %v", err)
				}
				if time.Now().After(deadline) {
					break
				}
			}
			if foreignConns.Load() != 0 || len(attachRequests(fx.hub, stored.Token)) != 0 {
				t.Fatal("the lease-only follower used a stored join URL")
			}
			mustAbsent(t, plain, "ordinary join state")
		})
	}
	t.Run("OrdinaryFollowStillRemembers", func(t *testing.T) {
		fx := newColdFixture(t, false)
		plain, leased := joinStatePaths(t)
		join := fx.hub.joinURL(fx.tray)
		cmd := exec.Command(binary, "-", "follow")
		cmd.Env = fx.env
		cmd.Stdin = strings.NewReader(join + "\n")
		log := &syncBuffer{}
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			if t.Failed() {
				t.Log(log.String())
			}
		})
		waitOrdinaryJoinSaved(t, plain, join, fx.tray.Token)
		data, err := os.ReadFile(plain)
		if err != nil || !strings.Contains(string(data), fx.tray.Token) {
			t.Fatalf("ordinary follow left no persisted join state: %v %s", err, data)
		}
		mustAbsent(t, leased, "lease join state")
	})
}
