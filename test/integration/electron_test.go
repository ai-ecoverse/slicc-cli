package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

func buildFakeElectron(t *testing.T, name string) browser.Installed {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe := filepath.Join(dir, name)
	build := exec.Command("go", "build", "-o", exe, "./testdata/fakeelectron")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		t.Fatal(err)
	}
	return browser.Installed{
		ID: "electron:fake", Name: "Fake", Engine: browser.EngineElectron,
		Path: exe, Executable: exe, DebugSupport: browser.DebugSupported,
	}
}

func launchFake(t *testing.T, in browser.Installed) *browser.Browser {
	t.Helper()
	b, err := browser.Launch(context.Background(), in, browser.LaunchOptions{LaunchTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if b.ProfileDir != "" || !strings.HasPrefix(b.CDP, "http://127.0.0.1:") || !strings.HasSuffix(b.WebSocketURL, "/devtools/browser/fake") || b.PID <= 0 {
		t.Errorf("%+v", b)
	}
	return b
}

func closeAndCheck(t *testing.T, b *browser.Browser) time.Duration {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	select {
	case <-b.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process still running after Close")
	}
	if _, err := getVersion(b.CDP); err == nil {
		t.Error("CDP still answers after Close")
	}
	return took
}

func TestElectronLaunchClosesWithBrowserClose(t *testing.T) {
	t.Setenv("FAKE_ELECTRON_MODE", "close")
	in := buildFakeElectron(t, "fakeclose")
	b := launchFake(t, in)
	if took := closeAndCheck(t, b); took > 3*time.Second {
		t.Errorf("Browser.close should end it quickly, took %s", took)
	}
}

func TestElectronLaunchEscalatesToKill(t *testing.T) {
	t.Setenv("FAKE_ELECTRON_MODE", "stubborn")
	in := buildFakeElectron(t, "fakestubborn")
	b := launchFake(t, in)
	closeAndCheck(t, b)
}

func TestElectronLaunchRefusesRunningApp(t *testing.T) {
	t.Setenv("FAKE_ELECTRON_MODE", "close")
	in := buildFakeElectron(t, "fakerunning")
	b := launchFake(t, in)
	defer closeAndCheck(t, b)
	if _, err := browser.Launch(context.Background(), in, browser.LaunchOptions{}); !errors.Is(err, browser.ErrAlreadyRunning) {
		t.Errorf("second launch: %v", err)
	}
	disabled := in
	disabled.DebugSupport = browser.DebugDisabled
	if _, err := browser.Launch(context.Background(), disabled, browser.LaunchOptions{}); err == nil || !strings.Contains(err.Error(), "blocks remote debugging") {
		t.Errorf("disabled app: %v", err)
	}
}

func TestElectronLaunchKillRestartsRunningApp(t *testing.T) {
	t.Setenv("FAKE_ELECTRON_MODE", "close")
	in := buildFakeElectron(t, "fakekill")
	old := launchFake(t, in)
	b, err := browser.Launch(context.Background(), in, browser.LaunchOptions{Kill: true, LaunchTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-old.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("the running app was not stopped")
	}
	if b.PID <= 0 || b.PID == old.PID || b.CDP == old.CDP {
		t.Errorf("restarted %+v, old %+v", b, old)
	}
	if _, err := getVersion(b.CDP); err != nil {
		t.Errorf("restarted app: %v", err)
	}
	closeAndCheck(t, b)
}
