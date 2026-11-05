package integration

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	reattaching = "lost the debugging connection to "
	reattached  = "reattached to "
)

func startReattachFlow(t *testing.T, f *crashBrowser) *flowProc {
	t.Helper()
	id := "cdp:" + strings.TrimPrefix(f.srv.URL, "http://")
	return startFlow(t, isolatedEnv(t, "NO_COLOR=1"), "--cdp", f.srv.URL, "--browser", id, "--plain", "--yes", "--no-kernel", "--no-jerk")
}

func (f *flowProc) running(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
		t.Fatalf("slicc exited (code %d):\n%s", f.code, f.stderr.String())
	default:
	}
}

func (f *flowProc) exitsWithin(t *testing.T, d time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	select {
	case <-f.done:
	case <-time.After(d):
		t.Fatalf("slicc still running %s later:\n%s", d, f.stderr.String())
	}
	return time.Since(start)
}

func TestTheDefaultFlowReattachesWhenTheBrowserDropsItsCDPConnection(t *testing.T) {
	f := newCrashBrowser(t)
	p := startReattachFlow(t, f)
	f.waitHello(t, 1)
	f.drop()
	p.waitStderr(t, reattaching)
	p.waitStderr(t, reattached)
	f.waitHello(t, 2)
	time.Sleep(300 * time.Millisecond)
	p.running(t)
	if out := p.stderr.String(); strings.Contains(out, "slicc: done") || strings.Contains(out, "seven tab closed") {
		t.Fatalf("a dropped connection ended the run:\n%s", out)
	}
}

func TestTheDefaultFlowKeepsTryingAnEndpointThatIsBrieflyUnavailable(t *testing.T) {
	f := newCrashBrowser(t)
	p := startReattachFlow(t, f)
	f.waitHello(t, 1)
	f.unavailable.Store(true)
	f.drop()
	p.waitStderr(t, reattaching)
	time.Sleep(2500 * time.Millisecond)
	p.running(t)
	if strings.Contains(p.stderr.String(), reattached) {
		t.Fatalf("reattached to an endpoint that refused:\n%s", p.stderr.String())
	}
	f.unavailable.Store(false)
	p.waitStderr(t, reattached)
	f.waitHello(t, 2)
	p.running(t)
}

func TestTheDefaultFlowEndsAtOnceWhenAnAttachedEndpointGoesAway(t *testing.T) {
	f := newCrashBrowser(t)
	p := startReattachFlow(t, f)
	f.waitHello(t, 1)
	_ = f.srv.Listener.Close()
	f.drop()
	if took := p.exitsWithin(t, 10*time.Second); took > 5*time.Second {
		t.Fatalf("slicc took %s to notice the endpoint was gone:\n%s", took, p.stderr.String())
	}
	out := p.stderr.String()
	for _, want := range []string{reattaching, " closed\n", "slicc: done"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stderr lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, reattached) {
		t.Fatalf("reattached to a closed endpoint:\n%s", out)
	}
	if p.code != 0 {
		t.Fatalf("exit %d", p.code)
	}
}

func TestStoppingTheDefaultFlowWhileItReattachesEndsItAtOnce(t *testing.T) {
	f := newCrashBrowser(t)
	p := startReattachFlow(t, f)
	f.waitHello(t, 1)
	f.unavailable.Store(true)
	f.drop()
	p.waitStderr(t, reattaching)
	if err := interrupt(p.cmd); err != nil {
		t.Fatal(err)
	}
	if took := p.exitsWithin(t, 15*time.Second); took > 5*time.Second {
		t.Fatalf("stopping during the reattach took %s:\n%s", took, p.stderr.String())
	}
	if p.code != 0 {
		t.Fatalf("exit %d:\n%s", p.code, p.stderr.String())
	}
}

func TestTheDefaultFlowEndsAtOnceWhenItsLaunchedBrowserDies(t *testing.T) {
	env, _ := launchEnv(t)
	env, id := testChrome(t, env)
	srv := pageServer(t)
	profile := "it-reattach-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	f := startFlow(t, append(env, "SLICC_DEBUG=1"), "--json", "--browser", id, "--profile", profile, "--headless", "--page", srv.URL+"/", "--no-kernel", "--no-jerk")
	f.next(t, "discovered")
	l := f.next(t, "launched")
	t.Cleanup(func() { _ = os.RemoveAll(l.ProfileDir) })
	for e := f.next(t, "attached"); e.Event != "linked"; {
		select {
		case e = <-f.events:
		case <-time.After(90 * time.Second):
			t.Fatalf("never linked:\n%s", f.stderr.String())
		}
	}
	proc, err := os.FindProcess(l.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for {
		e, ok := <-f.events
		if !ok {
			t.Fatalf("stdout closed before closed:\n%s", f.stderr.String())
		}
		if e.Event == "closed" {
			if len(e.Browsers) != 1 || e.Browsers[0] != id {
				t.Fatalf("closed %s", e.raw)
			}
			break
		}
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("closed %s after the browser died:\n%s", took, f.stderr.String())
	}
	if out := f.stderr.String(); !strings.Contains(out, "slicc debug: cdp connection to ") {
		t.Fatalf("the lost connection never reached the reattach check:\n%s", out)
	}
	if code := f.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
}
