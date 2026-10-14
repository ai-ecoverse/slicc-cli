package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

func launchedArgs(t *testing.T, log string, launch func() (*browser.Browser, error)) []string {
	t.Helper()
	b, err := launch()
	if err != nil {
		t.Fatal(err)
	}
	closeAndCheck(t, b)
	var logged struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal([]byte(readText(t, log)), &logged); err != nil {
		t.Fatal(err)
	}
	return logged.Args
}

func throwawayFlags(args []string) (bool, bool) {
	return slices.Contains(args, "--use-mock-keychain"), slices.Contains(args, "--password-store=basic")
}

func TestOnlyThrowawayProfilesGetAMockKeychain(t *testing.T) {
	in := buildFakeChrome(t)
	log := filepath.Join(t.TempDir(), "fakechrome.json")
	t.Setenv("FAKE_CHROME_LOG", log)
	h := newFakeHome(t)
	o := h.options()
	six := browser.SixProfileFor(o, 0)
	makeSixTree(t, six.Dir, "")
	temp := filepath.Join(t.TempDir(), "temp")
	if err := os.MkdirAll(temp, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, temp)
	}
	ctx := context.Background()
	launch := func(dir, profile string, headless bool) func() (*browser.Browser, error) {
		return func() (*browser.Browser, error) {
			lo := o
			lo.ProfilesDir = dir
			return browser.Launch(ctx, in, browser.LaunchOptions{Options: lo, Profile: profile, Headless: headless, LaunchTimeout: 20 * time.Second})
		}
	}
	cases := []struct {
		what   string
		launch func() (*browser.Browser, error)
		want   bool
	}{
		{"a SLICC profile in the temp dir", launch(filepath.Join(temp, "profiles"), "scratch", false), true},
		{"a headless SLICC profile in the temp dir", launch(filepath.Join(temp, "profiles"), "scratch-headless", true), true},
		{"a SLICC profile in the home", launch(h.profilesDir(), "mine", false), false},
		{"a headless SLICC profile in the home", launch(h.profilesDir(), "mine-headless", true), false},
		{"six's profile", func() (*browser.Browser, error) {
			return browser.LaunchSix(ctx, in, six, browser.LaunchOptions{Options: o, Headless: true, LaunchTimeout: 20 * time.Second})
		}, false},
	}
	for _, c := range cases {
		args := launchedArgs(t, log, c.launch)
		keychain, store := throwawayFlags(args)
		if keychain != c.want || store != c.want {
			t.Errorf("%s: --use-mock-keychain %v, --password-store=basic %v, want %v: %q", c.what, keychain, store, c.want, args)
		}
	}
	tempSix := browser.SixProfileFor(o, 0)
	tempSix.Dir = filepath.Join(temp, "six")
	makeSixTree(t, tempSix.Dir, "")
	args := launchedArgs(t, log, func() (*browser.Browser, error) {
		return browser.LaunchSix(ctx, in, tempSix, browser.LaunchOptions{Options: o, Headless: true, LaunchTimeout: 20 * time.Second})
	})
	if keychain, store := throwawayFlags(args); !keychain || !store {
		t.Errorf("a six profile in the temp dir: %q", args)
	}
}
