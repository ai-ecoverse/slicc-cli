package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
)

func (h fakeHome) legacyProfilesDir(t *testing.T) string {
	t.Helper()
	lower := filepath.Join(filepath.Dir(h.profilesDir()), "profiles")
	if runtime.GOOS != "darwin" {
		return lower
	}
	six := filepath.Join(h.root, "Library", "Application Support", "Slicc", "profiles")
	if err := os.MkdirAll(six, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lower); err != nil {
		t.Logf("case-sensitive volume: simulating six's folder at %s", lower)
		if err := os.MkdirAll(lower, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return lower
}

func (h fakeHome) getenv(key string) string {
	for _, kv := range h.env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestProfilesMoveOutOfSixsFolderOnce(t *testing.T) {
	h := newFakeHome(t)
	h.env = append(h.env, "SLICC_NO_UPDATE_CHECK=1", "SLICC_NO_TELEMETRY=1")
	legacy, root := h.legacyProfilesDir(t), h.profilesDir()
	six := newFakeCDP(t, "Chrome/150.0.7000.1", sevenPages())
	ours := newFakeCDP(t, "Chrome/150.0.7000.2", nil)
	writeFile(t, filepath.Join(legacy, "browser-coding-agent-chrome", "DevToolsActivePort"), six.activePort())
	writeFile(t, filepath.Join(legacy, "browser-coding-agent-chrome", "Default", "Preferences"), `{"six":true}`)
	writeFile(t, filepath.Join(legacy, "browser-coding-agent-chrome-5711", "Default", "Preferences"), `{"six":5711}`)
	writeFile(t, filepath.Join(legacy, ".profile-migration-complete"), "")
	writeFile(t, filepath.Join(legacy, "chrome", "DevToolsActivePort"), ours.activePort())
	writeFile(t, filepath.Join(legacy, "chrome", "Default", "Preferences"), `{"ours":true}`)
	held := runtime.GOOS != "windows"
	if held {
		if err := os.MkdirAll(filepath.Join(legacy, "held"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(fmt.Sprintf("testhost-%d", os.Getpid()), filepath.Join(legacy, "held", "SingletonLock")); err != nil {
			t.Fatal(err)
		}
	}

	r, errOut := browsersJSON(t, h.env)
	notice := fmt.Sprintf("slicc: profile held is in use by process %d, so it stays in %s; quit that browser, then move it to %s\n", os.Getpid(), legacy, root)
	if held && !strings.Contains(errOut, notice) {
		t.Fatalf("no notice for the held profile, stderr %q", errOut)
	}
	if entryByCDP(r, six.URL) != nil {
		t.Fatalf("six's DevToolsActivePort was listed: %+v", r.Entries)
	}
	got := entryByCDP(r, ours.URL)
	if got == nil || got.Endpoint.Profile != "chrome" || got.Endpoint.Source != browser.SourceActivePort {
		t.Fatalf("our moved profile wasn't found: %+v", r.Entries)
	}

	if s := readText(t, filepath.Join(root, "chrome", "Default", "Preferences")); s != `{"ours":true}` {
		t.Fatalf("our profile: %q", s)
	}
	if _, err := os.Stat(filepath.Join(legacy, "chrome")); err == nil {
		t.Fatal("our profile is still in the old folder")
	}
	if s := readText(t, filepath.Join(legacy, "browser-coding-agent-chrome", "Default", "Preferences")); s != `{"six":true}` {
		t.Fatalf("six's profile changed: %q", s)
	}
	if s := readText(t, filepath.Join(legacy, "browser-coding-agent-chrome", "DevToolsActivePort")); s != six.activePort() {
		t.Fatalf("six's DevToolsActivePort changed: %q", s)
	}
	readText(t, filepath.Join(legacy, "browser-coding-agent-chrome-5711", "Default", "Preferences"))
	readText(t, filepath.Join(legacy, ".profile-migration-complete"))
	for _, name := range []string{"browser-coding-agent-chrome", "browser-coding-agent-chrome-5711", ".profile-migration-complete"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			t.Fatalf("%s was moved", name)
		}
	}
	if held {
		if _, err := os.Lstat(filepath.Join(legacy, "held", "SingletonLock")); err != nil {
			t.Fatalf("the held profile moved: %v", err)
		}
	}

	o := browser.Options{Home: h.root, Getenv: h.getenv}
	if list := flow.Profiles(o, browser.Installed{ID: "chrome"}); !reflect.DeepEqual(list, []string{"chrome"}) {
		t.Fatalf("the picker lists %v", list)
	}

	writeFile(t, filepath.Join(legacy, "later", "Default", "Preferences"), `{}`)
	_, errOut = browsersJSON(t, h.env)
	if strings.Contains(errOut, "in use") {
		t.Fatalf("the migration ran twice: %q", errOut)
	}
	if _, err := os.Stat(filepath.Join(legacy, "later")); err != nil {
		t.Fatalf("a profile made after the migration moved: %v", err)
	}
}

func TestProfilesStartInSevenProfiles(t *testing.T) {
	h := newFakeHome(t)
	if notices := browser.MigrateProfiles(browser.Options{Home: h.root, Getenv: h.getenv}); len(notices) != 0 {
		t.Fatalf("%v", notices)
	}
	dir, err := browser.ProfileDir(browser.Options{Home: h.root, Getenv: h.getenv}, browser.Installed{ID: "chrome"}, "")
	if err != nil || dir != filepath.Join(h.profilesDir(), "chrome") {
		t.Fatalf("%s %v", dir, err)
	}
	if _, err := os.Stat(filepath.Dir(h.profilesDir())); err == nil {
		t.Fatal("migration created a folder with nothing to move")
	}
}
