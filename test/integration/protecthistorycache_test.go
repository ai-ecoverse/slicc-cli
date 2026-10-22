package integration

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCDPHistoryCacheOnlyRefusesAndNeverAuthorizes(t *testing.T) {
	for _, protected := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh ordinary", true: "fresh protected"}[protected], func(t *testing.T) {
			var reads atomic.Int32
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			env, fake := setupFakeProtect(t, func(seven, _ string) fakeScript {
				base := guardWaitScript("{}")
				return func(method string, params json.RawMessage) (string, []string) {
					if method == "Page.getNavigationHistory" {
						if reads.Add(1) == 1 {
							return `{"currentIndex":1,"entries":[{"id":7,"url":"` + seven + `/protected"},{"id":8,"url":"about:blank"}]}`, nil
						}
						close(started)
						<-release
						url := "about:blank"
						if protected {
							url = seven + "/new-protected"
						}
						return `{"currentIndex":0,"entries":[{"id":8,"url":"` + url + `"}]}`, nil
					}
					return base(method, params)
				}
			})
			var value any
			env.p.eval("window.historyCacheWork = linkTest.protect.historyCacheProof('chrome-2'); true", &value)
			env.p.waitFor("window.historyCacheReady === true", 10*time.Second)
			var local cdpOutcome
			env.p.eval("window.historyCacheLocal", &local)
			if !local.refused() || reads.Load() != 1 {
				t.Fatalf("cached protection used a browser round trip: %+v reads=%d", local, reads.Load())
			}
			forwarded := func() int {
				count := 0
				for _, raw := range fake.received() {
					var m struct {
						Method string `json:"method"`
					}
					_ = json.Unmarshal([]byte(raw), &m)
					if m.Method == "Page.navigateToHistoryEntry" {
						count++
					}
				}
				return count
			}
			if forwarded() != 0 {
				t.Fatal("cached protected entry was forwarded")
			}
			env.p.eval("window.historyCacheContinue(); true", &value)
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("uncached entry did not request fresh history")
			}
			var settled bool
			env.p.eval("window.historyCacheFresh !== undefined", &settled)
			if settled || forwarded() != 0 || reads.Load() != 2 {
				t.Fatal("uncached entry was decided without fresh evidence")
			}
			unblock()
			env.p.waitFor("window.historyCacheFresh !== undefined", 10*time.Second)
			var fresh cdpOutcome
			env.p.eval("window.historyCacheFresh", &fresh)
			if protected {
				if !fresh.refused() || forwarded() != 0 {
					t.Fatalf("new protected entry was not refused: %+v", fresh)
				}
			} else if !fresh.OK || forwarded() != 1 {
				t.Fatalf("fresh clean evidence did not permit navigation: %+v", fresh)
			}
		})
	}
}
