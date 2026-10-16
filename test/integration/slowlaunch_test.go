package integration

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

type slowReports struct {
	mu   sync.Mutex
	seen []time.Duration
}

func (s *slowReports) add(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, d)
}

func (s *slowReports) list() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.seen...)
}

func TestLaunchWaitsForAChromeThatStartsSlowly(t *testing.T) {
	in := buildFakeChrome(t)
	t.Setenv("FAKE_CHROME_DELAY_MS", "40000")
	var slow slowReports
	start := time.Now()
	b, err := browser.Launch(context.Background(), in, browser.LaunchOptions{Options: newFakeHome(t).options(), Profile: "slow", Headless: true, Slow: slow.add})
	took := time.Since(start)
	if err != nil {
		t.Fatalf("after %s: %v", took, err)
	}
	closeAndCheck(t, b)
	if took < 40*time.Second {
		t.Fatalf("the launch returned after %s, before the fake wrote DevToolsActivePort", took)
	}
	seen := slow.list()
	if len(seen) < 3 || seen[0] != 10*time.Second || seen[1] != 20*time.Second || seen[2] != 30*time.Second {
		t.Fatalf("slow reports %v, want every 10 s from 10 s on", seen)
	}
	for i, d := range seen {
		if d != time.Duration(i+1)*browser.SlowLaunchAfter || d > took {
			t.Fatalf("slow reports %v over a %s launch", seen, took)
		}
	}
}

func TestLaunchFailsAtOnceWhenChromeExits(t *testing.T) {
	in := buildFakeChrome(t)
	t.Setenv("FAKE_CHROME_EXIT", "3")
	t.Setenv("FAKE_CHROME_EXIT_MESSAGE", "fake chrome: the profile is locked by another process")
	var slow slowReports
	start := time.Now()
	_, err := browser.Launch(context.Background(), in, browser.LaunchOptions{Options: newFakeHome(t).options(), Profile: "quits", Headless: true, Slow: slow.add})
	took := time.Since(start)
	if err == nil {
		t.Fatal("a Chrome that exits launched")
	}
	if took > 5*time.Second {
		t.Fatalf("the failure took %s: %v", took, err)
	}
	for _, want := range []string{"Fake Chrome exited with code 3", "before writing DevToolsActivePort", "fake chrome: the profile is locked by another process"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
	if seen := slow.list(); len(seen) != 0 {
		t.Fatalf("slow reports %v for a launch that failed at once", seen)
	}
}

func TestLaunchGivesUpAtItsCeilingWhileChromeRuns(t *testing.T) {
	in := buildFakeChrome(t)
	t.Setenv("FAKE_CHROME_DELAY_MS", "60000")
	start := time.Now()
	_, err := browser.Launch(context.Background(), in, browser.LaunchOptions{Options: newFakeHome(t).options(), Profile: "stuck", Headless: true, LaunchTimeout: 2 * time.Second})
	took := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "Fake Chrome is still running after") || !strings.Contains(err.Error(), "has not written") {
		t.Fatalf("after %s: %v", took, err)
	}
	if took > 15*time.Second {
		t.Fatalf("the ceiling of 2 s took %s", took)
	}
}
