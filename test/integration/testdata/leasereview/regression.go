package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/lease"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

func leaseReviewHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	rt := filepath.Join(home, "run")
	if err := os.MkdirAll(rt, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Setenv("TMPDIR", rt)
	if dir, err := ctl.Dir(); err != nil || !strings.HasPrefix(dir, rt+string(filepath.Separator)) {
		rt, err = os.MkdirTemp("/tmp", "sl-rt-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(rt) })
		t.Setenv("XDG_RUNTIME_DIR", rt)
		t.Setenv("TMPDIR", rt)
		if dir, err := ctl.Dir(); err != nil || !strings.HasPrefix(dir, rt+string(filepath.Separator)) {
			t.Fatalf("control directory %q escapes the isolated runtime directory", dir)
		}
	}
}

func leaseReviewCtlDir(t *testing.T) string {
	t.Helper()
	dir, err := ctl.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLeaseReviewHubOrigin(t *testing.T) {
	for _, row := range []struct {
		name, typed, join, canon string
		same                     bool
	}{
		{"Case", "HTTPS://Hub.Example", "https://hub.example/join/t", "https://hub.example", true},
		{"CaseJoin", "https://hub.example", "https://HUB.example/join/t", "https://hub.example", true},
		{"Punycode", "https://BÜCHER.example", "https://xn--bcher-kva.example/join/t", "https://xn--bcher-kva.example", true},
		{"DefaultPortTyped", "https://hub.example:443", "https://hub.example/join/t", "https://hub.example", true},
		{"DefaultPortJoin", "https://hub.example/", "https://hub.example:443/join/t", "https://hub.example", true},
		{"TrailingDotTyped", "https://hub.example.", "https://hub.example/join/t", "https://hub.example", true},
		{"TrailingDotJoin", "https://hub.example", "https://hub.example./join/t", "https://hub.example", true},
		{"DoubleTrailingDot", "https://hub.example..", "", "", false},
		{"UserInfo", "https://user@hub.example", "", "", false},
		{"UserInfoPassword", "https://user:pw@hub.example", "", "", false},
		{"Path", "https://hub.example/seven", "", "", false},
		{"JoinURLTyped", "https://hub.example/join/t", "", "", false},
		{"Query", "https://hub.example/?", "", "", false},
		{"Fragment", "https://hub.example#x", "", "", false},
		{"NoScheme", "hub.example", "", "", false},
		{"Empty", "", "", "", false},
		{"EmptyPort", "https://hub.example:", "", "", false},
		{"OtherScheme", "ftp://hub.example", "", "", false},
		{"PublicHTTP", "http://hub.example", "", "", false},
		{"LoopbackHTTP", "http://127.0.0.1:8080", "http://127.0.0.1:8080/join/t", "http://127.0.0.1:8080", true},
		{"LoopbackHTTPDefaultPort", "http://LOCALHOST:80", "http://localhost/join/t", "http://localhost", true},
		{"LoopbackIPv6", "http://[::1]:9", "http://[::1]:9/join/t", "http://[::1]:9", true},
		{"LoopbackScheme", "https://127.0.0.1:9", "http://127.0.0.1:9/join/t", "https://127.0.0.1:9", false},
		{"LookalikeSuffix", "https://hub.example", "https://hub.example.evil.test/join/t", "https://hub.example", false},
		{"LookalikeHyphen", "https://hub.example", "https://hub-example.test/join/t", "https://hub.example", false},
		{"LookalikeCyrillic", "https://hub.example", "https://hub.\u0435xample/join/t", "https://hub.example", false},
		{"PortTyped", "https://hub.example:8443", "https://hub.example/join/t", "https://hub.example:8443", false},
		{"PortJoin", "https://hub.example", "https://hub.example:8443/join/t", "https://hub.example", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			got, err := reviewHubOrigin(row.typed)
			if row.canon == "" {
				if err == nil {
					t.Fatalf("%q was accepted as %q", row.typed, got)
				}
				return
			}
			if err != nil || got != row.canon {
				t.Fatalf("%q: got %q, %v; want %q", row.typed, got, err, row.canon)
			}
			if sameLeaseHub(row.join, got) != row.same {
				t.Fatalf("join on %q against %q: same=%v", joinHubOrigin(row.join), got, !row.same)
			}
		})
	}
}

func TestLeaseReviewIdentity(t *testing.T) {
	leaseReviewHome(t)
	const join = "https://Hub.Example:443/join/tok"
	pin, err := leaseDevice("https://hub.example")
	if err != nil || len(pin) != 64 || strings.ToLower(pin) != pin {
		t.Fatalf("pre-dial pin %q: %v", pin, err)
	}
	again, err := leaseDevice("https://hub.example")
	if err != nil || again != pin {
		t.Fatal("a second load created another identity")
	}
	base, err := tray.DefaultCertsPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{base + ".lease", base + ".lease.lock"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("%s has mode %v", filepath.Base(path), info.Mode().Perm())
		}
	}
	if _, err := os.Stat(base); err == nil {
		t.Fatal("the pre-dial pin touched the ordinary device store")
	}
	known := tray.OpenCerts(base + ".lease").Known()
	if len(known) != 1 || known[0].Origin != "https://hub.example:443" {
		t.Fatalf("lease store holds %d identities", len(known))
	}
	r := &followRun{joinURL: join, hub: tray.HubOrigin(join), status: func(string, ...any) {}}
	if r.certs, err = tray.OpenDefaultCerts(); err != nil {
		t.Fatal(err)
	}
	if r.state, err = tray.OpenDefaultState(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.leaseIsolation(followFlags{leaseBrowser: "http://127.0.0.1:9222"}, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.certificate(); err != nil {
		t.Fatal(err)
	}
	if r.device != pin || len(tray.OpenCerts(base+".lease").Known()) != 1 {
		t.Fatalf("follower identity %q differs from the pre-dial pin %q", r.device, pin)
	}
	plain := &followRun{joinURL: join, hub: tray.HubOrigin(join), status: func(string, ...any) {}, certs: tray.OpenCerts(base)}
	if _, err := plain.certificate(); err != nil || plain.device == pin {
		t.Fatal("ordinary follow shares the lease identity")
	}
}

func TestLeaseReviewIdentityUpgrade(t *testing.T) {
	leaseReviewHome(t)
	base, err := tray.DefaultCertsPath()
	if err != nil {
		t.Fatal(err)
	}
	const join = "https://hub.example/join/tok"
	if _, _, err := tray.OpenCerts(base + ".lease").Load(tray.HubOrigin(join)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(base + ".lease")
	if err != nil {
		t.Fatal(err)
	}
	known := tray.OpenCerts(base + ".lease").Known()
	if len(known) != 1 || known[0].Origin != "https://hub.example:443" {
		t.Fatal("the seeded store does not use the released key")
	}
	pin, err := leaseDevice("https://hub.example")
	if err != nil || pin != known[0].Fingerprint {
		t.Fatalf("pre-dial pin %q differs from the stored identity %q: %v", pin, known[0].Fingerprint, err)
	}
	r := &followRun{joinURL: join, hub: tray.HubOrigin(join), status: func(string, ...any) {}, certs: tray.OpenCerts(base)}
	if _, err := r.leaseIsolation(followFlags{leaseBrowser: "http://127.0.0.1:9222"}, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.certificate(); err != nil || r.device != pin {
		t.Fatalf("follower identity %q differs from the pre-dial pin %q", r.device, pin)
	}
	after, err := os.ReadFile(base + ".lease")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("loading the stored identity rewrote the store")
	}
}

func TestLeaseReviewPrompts(t *testing.T) {
	const sevenPin = "1111111111111111111111111111111111111111111111111111111111111111"
	const otherPin = "2222222222222222222222222222222222222222222222222222222222222222"
	const join = "https://hub.example/join/secret-token"
	f := ownerFlags{op: "review", to: "jkt:x"}
	run := func(input, secret string) (string, *coldPair, error) {
		var out bytes.Buffer
		cp := &coldPair{rd: bufio.NewReader(strings.NewReader(input)), out: &out, secret: func(show func()) ([]byte, error) {
			show()
			if secret == "" {
				return nil, os.ErrClosed
			}
			return []byte(secret), nil
		}}
		_, err := cp.pair(f, nil)
		return out.String(), cp, err
	}
	full := "http://127.0.0.1:9222\nfirst\nhttps://hub.example\n" + sevenPin + "\n"
	t.Run("Order", func(t *testing.T) {
		leaseReviewHome(t)
		pin, err := leaseDevice("https://hub.example")
		if err != nil {
			t.Fatal(err)
		}
		out, cp, err := run(full, "https://other.example/join/secret-token")
		if err == nil || !strings.Contains(err.Error(), "not on https://hub.example") || cp.child != nil {
			t.Fatalf("a join URL on another hub was not refused before any start: %v", err)
		}
		order := []string{"loopback HTTP origin:", "Local link label:", "Tray hub origin", "Broker SHA-256 fingerprint for https://hub.example: " + pin + "\n", "Nothing has been dialed yet.", "Expected Seven DTLS fingerprint", "join URL here (hidden):"}
		at := 0
		for _, want := range order {
			i := strings.Index(out[at:], want)
			if i < 0 {
				t.Fatalf("missing %q in order:\n%s", want, out)
			}
			at += i + len(want)
		}
		if strings.Contains(out, "secret-token") || strings.Contains(out, "PRIVATE KEY") {
			t.Fatal("the prompts echoed a secret")
		}
	})
	for cut, name := range []string{"EOFAtBrowser", "EOFAtLabel", "EOFAtHub", "EOFAtSevenPin", "EOFAtJoinURL"} {
		t.Run(name, func(t *testing.T) {
			leaseReviewHome(t)
			lines := strings.SplitAfter(full, "\n")
			_, cp, err := run(strings.Join(lines[:cut], ""), "")
			if err == nil || cp.child != nil {
				t.Fatalf("EOF after %d answers did not abort before any start: %v", cut, err)
			}
			base, _ := tray.DefaultCertsPath()
			if _, err := os.Stat(base + ".lease"); (err == nil) != (cut >= 3) {
				t.Fatalf("EOF after %d answers: identity present=%v", cut, err == nil)
			}
		})
	}
	t.Run("JoinHostNotCanonical", func(t *testing.T) {
		leaseReviewHome(t)
		for _, row := range []struct{ hub, secret string }{
			{"https://hub.example", "https://hub.example./join/secret-token"},
			{"https://bücher.example", "https://bücher.example/join/secret-token"},
		} {
			input := "http://127.0.0.1:9222\ncanon\n" + row.hub + "\n" + sevenPin + "\n"
			out, cp, err := run(input, row.secret)
			if err == nil || !strings.Contains(err.Error(), "as the join URL spells") || cp.child != nil {
				t.Fatalf("%s / %s was not refused before start: %v\n%s", row.hub, row.secret, err, out)
			}
			base, err := tray.DefaultCertsPath()
			if err != nil {
				t.Fatal(err)
			}
			for _, k := range tray.OpenCerts(base + ".lease").Known() {
				if strings.Contains(k.Origin, "bücher") || strings.Contains(k.Origin, "example.:") || strings.HasSuffix(k.Origin, "example.") {
					t.Fatalf("store kept a non-canonical key %q", k.Origin)
				}
			}
		}
	})
	t.Run("PinConflict", func(t *testing.T) {
		leaseReviewHome(t)
		if err := pairPin("first", sevenPin); err != nil {
			t.Fatal(err)
		}
		_, cp, err := run("http://127.0.0.1:9222\nfirst\nhttps://other.example\n"+otherPin+"\n", join)
		if err == nil || !strings.Contains(err.Error(), "choose a new label") || cp.child != nil {
			t.Fatalf("a different pin under a stored label was accepted: %v", err)
		}
		_, cp, err = run("http://127.0.0.1:9222\nfirst\nhttps://other.example\n"+sevenPin+"\n", join)
		if err == nil || !strings.Contains(err.Error(), "not on https://other.example") || cp.child != nil {
			t.Fatalf("the retyped pin did not reach the join URL check: %v", err)
		}
	})
	t.Run("FingerprintWrite", func(t *testing.T) {
		leaseReviewHome(t)
		input := "http://127.0.0.1:9222\nfirst\nhttps://hub.example\n"
		for _, row := range []struct{ needle, want string }{
			{"Broker SHA-256 fingerprint", "could not show the broker fingerprint"},
			{"Nothing has been dialed yet", "could not show the broker fingerprint"},
			{"Expected Seven DTLS fingerprint", "Seven fingerprint prompt"},
		} {
			out := &failOn{needle: row.needle}
			cp := &coldPair{rd: bufio.NewReader(strings.NewReader(input)), out: out}
			_, err := cp.pair(f, nil)
			if err == nil || !strings.Contains(err.Error(), row.want) || cp.child != nil {
				t.Fatalf("%q write failure continued: %v", row.needle, err)
			}
		}
	})
}

type failOn struct {
	needle string
}

func (w *failOn) Write(p []byte) (int, error) {
	if strings.Contains(string(p), w.needle) {
		return 0, errors.New("full")
	}
	return len(p), nil
}

func TestLeaseReviewFollowerJoinState(t *testing.T) {
	leaseReviewHome(t)
	const join = "https://hub.example/join/tok"
	for _, leased := range []bool{true, false} {
		r := &followRun{joinURL: join, hub: tray.HubOrigin(join), status: func(string, ...any) {}}
		var err error
		if r.certs, err = tray.OpenDefaultCerts(); err != nil {
			t.Fatal(err)
		}
		if r.state, err = tray.OpenDefaultState(); err != nil {
			t.Fatal(err)
		}
		ff := followFlags{}
		if leased {
			ff.leaseBrowser = "http://127.0.0.1:9222"
		}
		if _, err := r.leaseIsolation(ff, func(string, ...any) {}); err != nil {
			t.Fatal(err)
		}
		if (r.state == nil) != leased {
			t.Fatalf("lease-only=%v keeps join state %v", leased, r.state != nil)
		}
		if leased && r.leaseClaim != nil {
			_ = r.leaseClaim.Close()
			r.leaseClaim = nil
		}
	}
}

func TestLeaseReviewPathlessErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	base, err := tray.DefaultCertsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Dir(base)), 0o700); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Dir(base)
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = leaseDevice("https://hub.example")
	if err == nil {
		t.Fatal("expected a config error")
	}
	if strings.Contains(err.Error(), blocked) || strings.Contains(err.Error(), home) {
		t.Fatalf("error names a home path: %v", err)
	}
}

func TestLeaseReviewStoreModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes")
	}
	leaseReviewHome(t)
	const origin = "https://hub.example"
	pin, err := leaseDevice(origin)
	if err != nil {
		t.Fatal(err)
	}
	base, err := tray.DefaultCertsPath()
	if err != nil {
		t.Fatal(err)
	}
	store, dir := base+".lease", filepath.Dir(base)
	if err := os.Chmod(store, 0o644); err != nil || os.Chmod(dir, 0o755) != nil {
		t.Fatal(err)
	}
	again, err := leaseDevice(origin)
	if err != nil || again != pin {
		t.Fatalf("loose store: %v pin=%q", err, again)
	}
	for _, path := range []string{store, store + ".lock"} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", filepath.Base(path), info.Mode().Perm())
		}
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("config dir mode %v", info.Mode().Perm())
	}
}

func TestLeaseReviewStoreFailClosed(t *testing.T) {
	leaseReviewHome(t)
	base, err := tray.DefaultCertsPath()
	if err != nil {
		t.Fatal(err)
	}
	store := base + ".lease"
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := leaseDevice("https://hub.example")
	if err == nil || pin != "" || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("damaged store: pin=%q err=%v", pin, err)
	}
	if strings.Contains(err.Error(), filepath.Dir(store)) || strings.Contains(err.Error(), store) {
		t.Fatalf("error names a path: %v", err)
	}
	after, err := os.ReadFile(store)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("damaged store was replaced")
	}
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(store, []byte(`{"version":1,"hubs":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(store, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(store, 0o600) })
		pin, err = leaseDevice("https://hub.example")
		if err == nil || pin != "" || !strings.Contains(err.Error(), "cannot be read") && !strings.Contains(err.Error(), "cannot be secured") {
			t.Fatalf("unreadable store: pin=%q err=%v", pin, err)
		}
		if err := os.Chmod(store, 0o600); err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(map[string]any{"version": 1, "hubs": []map[string]string{{"origin": "https://hub.example:443", "pem": "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n", "createdAt": "2026-01-01T00:00:00Z"}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(store, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(store, 0o400); err != nil {
			t.Fatal(err)
		}
		pin, err = leaseDevice("https://hub.example")
		if err == nil || pin != "" || !strings.Contains(err.Error(), "cannot be read") && !strings.Contains(err.Error(), "cannot be secured") {
			t.Fatalf("0400 store: pin=%q err=%v", pin, err)
		}
		after, err := os.ReadFile(store)
		if err != nil || !bytes.Equal(body, after) {
			t.Fatal("0400 store was replaced")
		}
		if err := os.Chmod(store, 0o600); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Dir(store)
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		pin, err = leaseDevice("https://hub.example")
		if err == nil || pin != "" || !strings.Contains(err.Error(), "cannot be read") && !strings.Contains(err.Error(), "cannot be secured") {
			t.Fatalf("0500 dir: pin=%q err=%v", pin, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct{ name, pem string }{{"NotAPEM", "not-a-pem"}, {"EmptyPEM", ""}} {
		if err := os.Chmod(store, 0o600); err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(map[string]any{"version": 1, "hubs": []map[string]string{{"origin": "https://hub.example:443", "pem": row.pem, "createdAt": "2026-01-01T00:00:00Z"}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(store, body, 0o600); err != nil {
			t.Fatal(err)
		}
		pin, err = leaseDevice("https://hub.example")
		if err == nil || pin != "" || !strings.Contains(err.Error(), "damaged") {
			t.Fatalf("%s: unusable certificate was accepted: pin=%q err=%v", row.name, pin, err)
		}
		if strings.Contains(err.Error(), store) {
			t.Fatalf("%s: error names a path: %v", row.name, err)
		}
		after, err := os.ReadFile(store)
		if err != nil || !bytes.Equal(body, after) {
			t.Fatalf("%s: unusable certificate was replaced", row.name)
		}
	}
	plain := tray.OpenCerts(base)
	if _, _, err := plain.Load("https://hub.example:443"); err != nil {
		t.Fatalf("ordinary follow load: %v", err)
	}
}

func TestLeaseReviewDeviceClaim(t *testing.T) {
	leaseReviewHome(t)
	const join = "https://hub.example/join/tok"
	origin := "https://hub.example"
	if _, err := leaseDevice(origin); err != nil {
		t.Fatal(err)
	}
	base, err := tray.DefaultCertsPath()
	if err != nil {
		t.Fatal(err)
	}
	first := &followRun{joinURL: join, hub: tray.HubOrigin(join), status: func(string, ...any) {}, certs: leaseCertStore(base)}
	if err := first.claimLeaseDevice(); err != nil || first.leaseClaim == nil {
		t.Fatalf("first claim: %v", err)
	}
	claimPath := leaseClaimFile(first.certs.Path(), tray.HubOrigin(join))
	if _, err := os.Stat(claimPath); err != nil {
		t.Fatal(err)
	}
	second := &followRun{joinURL: join, hub: tray.HubOrigin(join), status: func(string, ...any) {}, certs: leaseCertStore(base)}
	if err := second.claimLeaseDevice(); !errors.Is(err, errLeaseClaimHeld) || second.leaseClaim != nil {
		t.Fatalf("second claim must refuse without holding: %v", err)
	}
	_ = first.leaseClaim.Close()
	first.leaseClaim = nil
	third := &followRun{joinURL: join, hub: tray.HubOrigin(join), status: func(string, ...any) {}, certs: leaseCertStore(base)}
	if err := third.claimLeaseDevice(); err != nil || third.leaseClaim == nil {
		t.Fatalf("claim after release: %v", err)
	}
	_ = third.leaseClaim.Close()
	if runtime.GOOS != "windows" {
		target := claimPath + ".target"
		if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(claimPath)
		if err := os.Symlink(target, claimPath); err != nil {
			t.Fatal(err)
		}
		linked := &followRun{joinURL: join, hub: tray.HubOrigin(join), status: func(string, ...any) {}, certs: leaseCertStore(base)}
		if err := linked.claimLeaseDevice(); err == nil || linked.leaseClaim != nil {
			t.Fatalf("symlink claim must refuse: %v", err)
		}
		lock := base + ".lease.lock"
		_ = os.Remove(lock)
		if err := os.Symlink(target, lock); err != nil {
			t.Fatal(err)
		}
		if _, err := leaseDevice(origin); err == nil || !strings.Contains(err.Error(), "cannot be secured") && !strings.Contains(err.Error(), "cannot be read") {
			t.Fatalf("symlink lock must refuse: %v", err)
		}
	}
	plain := &followRun{joinURL: join, hub: tray.HubOrigin(join), status: func(string, ...any) {}}
	if plain.certs, err = tray.OpenDefaultCerts(); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.leaseIsolation(followFlags{}, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if plain.leaseCert || plain.leaseClaim != nil {
		t.Fatal("ordinary follow took the lease claim path")
	}
}

func TestLeaseReviewChildCallAfterExit(t *testing.T) {
	leaseReviewHome(t)
	path := filepath.Join(leaseReviewCtlDir(t), "sl-hung-"+strconv.Itoa(os.Getpid())+".sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close(); _ = os.Remove(path) })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer func() { _ = c.Close() }(); time.Sleep(time.Minute) }()
		}
	}()
	exited := make(chan struct{})
	close(exited)
	cp := &coldPair{child: &leaseChild{proc: &os.Process{Pid: 1}, exited: exited, path: path}}
	done := make(chan error, 1)
	go func() {
		done <- cp.childCall(selection{path: path, pid: 1}, ctl.Request{Op: ctl.OpStop})
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "stopped") {
			t.Fatalf("childCall after exit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("childCall dialed a hung socket after the child exited")
	}
}

type foreignSocket struct {
	path string
	ln   net.Listener
}

func foreignAnswer(t *testing.T, path string, pid int) *foreignSocket {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	st := ctl.NewHolder("follow", "fake").Snapshot()
	st.PID, st.State, st.Offers = pid, ctl.StateConnected, []string{lease.Cap}
	body, _ := json.Marshal(st)
	reply := append(slices.Clone(body), '\n')
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				if _, err := bufio.NewReader(c).ReadString('\n'); err == nil {
					_, _ = c.Write(reply)
				}
			}()
		}
	}()
	return &foreignSocket{path: path, ln: ln}
}

func (f *foreignSocket) answers(t *testing.T) bool {
	t.Helper()
	c, err := net.DialTimeout("unix", f.path, 2*time.Second)
	if err != nil {
		return false
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte("{\"v\":1,\"op\":\"status\"}\n")); err != nil {
		return false
	}
	_, err = bufio.NewReader(c).ReadString('\n')
	return err == nil
}

func TestLeaseReviewStopLeavesForeign(t *testing.T) {
	leaseReviewHome(t)
	dir := leaseReviewCtlDir(t)
	other := exec.Command("sleep", "60")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	exited := make(chan struct{})
	go func() { _, _ = other.Process.Wait(); close(exited) }()
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	foreignPath := filepath.Join(dir, strconv.Itoa(other.Process.Pid)+".sock")
	foreign := foreignAnswer(t, foreignPath, other.Process.Pid)
	if err := os.WriteFile(foreignPath+".lock", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !foreign.answers(t) {
		t.Fatal("the foreign socket does not answer")
	}
	live := &leaseChild{proc: other.Process, exited: exited, path: foreignPath}
	if !live.alive() {
		t.Fatal("expected a live child")
	}
	live.stop()
	if !exists(foreignPath) || !exists(foreignPath+".lock") || !foreign.answers(t) {
		t.Fatal("stop unlinked a foreign control socket by name")
	}
}

type interruptBroker struct {
	cp      *coldPair
	child   *leaseChild
	stopped chan struct{}
	exited  chan struct{}
}

func newInterruptBroker(t *testing.T) *interruptBroker {
	t.Helper()
	h := ctl.NewHolder("follow", "fake")
	h.Answer(ctl.OpLeaseList, func(ctl.Request) map[string]any {
		return map[string]any{"ok": true, "pending": []lease.Pending{}, "active": []lease.Active{}}
	})
	b := &interruptBroker{stopped: make(chan struct{}), exited: make(chan struct{})}
	h.Handle(ctl.OpStop, func() {
		close(b.stopped)
		close(b.exited)
	})
	srv, err := ctl.Serve(h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	b.child = &leaseChild{proc: &os.Process{Pid: os.Getpid()}, exited: b.exited, path: srv.Path()}
	b.cp = &coldPair{approving: true, out: ioDiscard{}, child: b.child}
	return b
}

func (b *interruptBroker) wasStopped() bool {
	select {
	case <-b.stopped:
		return true
	default:
		return false
	}
}

func TestLeaseReviewInterruptAfterGrant(t *testing.T) {
	leaseReviewHome(t)
	t.Run("KeptFlag", func(t *testing.T) {
		cp := &coldPair{kept: true, out: ioDiscard{}}
		if msg := cp.onInterrupt(); !strings.Contains(msg, "was kept") || !strings.Contains(msg, "approved") || strings.Contains(msg, "nothing was issued") || strings.Contains(msg, "unknown") {
			t.Fatalf("after grant: %q", msg)
		}
	})
	t.Run("WhileApproving", func(t *testing.T) {
		b := newInterruptBroker(t)
		msg := b.cp.onInterrupt()
		if !strings.Contains(msg, "unknown") || !strings.Contains(msg, "was kept") || !strings.Contains(msg, "lease status") || strings.Contains(msg, "nothing was issued") || strings.Contains(msg, "lease list") {
			t.Fatalf("approving interrupt: %q", msg)
		}
		if !b.cp.kept || b.cp.child != b.child || b.wasStopped() {
			t.Fatal("an in-flight approval stopped the broker")
		}
	})
	t.Run("BeforeApprove", func(t *testing.T) {
		exited := make(chan struct{})
		close(exited)
		cp := &coldPair{approving: false, child: &leaseChild{proc: &os.Process{Pid: 1}, exited: exited}, out: ioDiscard{}}
		if msg := cp.onInterrupt(); !strings.Contains(msg, "nothing was issued") {
			t.Fatalf("before approve: %q", msg)
		}
	})
}

func TestLeaseReviewApproveInterruptRace(t *testing.T) {
	leaseReviewHome(t)
	t.Run("InterruptBeforeBegin", func(t *testing.T) {
		called := false
		h := ctl.NewHolder("follow", "fake")
		h.Answer(ctl.OpLeaseApprove, func(ctl.Request) map[string]any {
			called = true
			return map[string]any{"ok": true}
		})
		exited := make(chan struct{})
		srv, err := ctl.Serve(h)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(srv.Close)
		cp := &coldPair{interrupted: true, out: ioDiscard{}, child: &leaseChild{proc: &os.Process{Pid: os.Getpid()}, exited: exited, path: srv.Path()}}
		sel := selection{path: srv.Path(), pid: os.Getpid(), p: lease.Pending{Request: "r1", TTL: 300, JKT: strings.Repeat("A", 43)}}
		if err := cp.approve(sel); !errors.Is(err, errInterrupted) || called || cp.approving {
			t.Fatalf("interrupt before begin: err=%v called=%v approving=%v", err, called, cp.approving)
		}
	})
	t.Run("InterruptAfterBegin", func(t *testing.T) {
		b := newInterruptBroker(t)
		if !b.cp.beginApprove() {
			t.Fatal("beginApprove refused")
		}
		msg := b.cp.onInterrupt()
		if !strings.Contains(msg, "unknown") || !strings.Contains(msg, "was kept") || strings.Contains(msg, "nothing was issued") {
			t.Fatalf("interrupt after begin: %q", msg)
		}
		if !b.cp.kept || b.wasStopped() {
			t.Fatal("interrupt after begin stopped the broker")
		}
	})
	t.Run("InterruptDuringFinish", func(t *testing.T) {
		called := false
		h := ctl.NewHolder("follow", "fake")
		h.Answer(ctl.OpLeaseApprove, func(ctl.Request) map[string]any {
			called = true
			return map[string]any{"ok": true}
		})
		exited := make(chan struct{})
		srv, err := ctl.Serve(h)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(srv.Close)
		out := &bytes.Buffer{}
		cp := &coldPair{out: out, child: &leaseChild{proc: &os.Process{Pid: os.Getpid()}, exited: exited, path: srv.Path()}}
		sel := selection{path: srv.Path(), pid: os.Getpid(), p: lease.Pending{Request: "r1", TTL: 300, JKT: strings.Repeat("A", 43)}}
		var sawApproving, sawKept bool
		afterApproveReply = func() {
			cp.mu.Lock()
			sawApproving, sawKept = cp.approving, cp.kept
			cp.interrupted = true
			cp.mu.Unlock()
		}
		t.Cleanup(func() { afterApproveReply = nil })
		if err := cp.approve(sel); err != nil || !called {
			t.Fatalf("approve during finish: err=%v called=%v", err, called)
		}
		if !sawApproving && !sawKept {
			t.Fatal("completion window had neither approving nor kept")
		}
		if !cp.kept || !strings.Contains(out.String(), "Approved for") {
			t.Fatalf("finish race: kept=%v out=%q", cp.kept, out.String())
		}
		msg := cp.onInterrupt()
		if strings.Contains(msg, "nothing was issued") || !strings.Contains(msg, "was kept") {
			t.Fatalf("after finish interrupt: %q", msg)
		}
	})
	t.Run("ApprovingFinishInterleave", func(t *testing.T) {
		b := newInterruptBroker(t)
		if !b.cp.beginApprove() {
			t.Fatal("beginApprove refused")
		}
		var finished bool
		afterInterruptSnap = func() {
			finished = true
			b.cp.finishApprove(approveGranted)
		}
		t.Cleanup(func() { afterInterruptSnap = nil })
		msg := b.cp.onInterrupt()
		if finished {
			if strings.Contains(msg, "unknown") || b.cp.approveUnknown {
				t.Fatalf("stale approving snap overwrote a finished grant: %q unknown=%v", msg, b.cp.approveUnknown)
			}
			if !strings.Contains(msg, "approved") || !strings.Contains(msg, "was kept") {
				t.Fatalf("finished grant interrupt: %q", msg)
			}
			return
		}
		if !strings.Contains(msg, "unknown") || !b.cp.approveUnknown || !b.cp.kept {
			t.Fatalf("approving interrupt without snap gap: %q kept=%v unknown=%v", msg, b.cp.kept, b.cp.approveUnknown)
		}
	})
}

func TestLeaseReviewStartInterrupted(t *testing.T) {
	leaseReviewHome(t)
	cp := &coldPair{interrupted: true, out: ioDiscard{}}
	c, err := cp.start("http://127.0.0.1:9", "label", "ab", []byte("https://hub.example/join/tok"))
	if !errors.Is(err, errInterrupted) || c != nil || cp.child != nil {
		t.Fatalf("start after interrupt: c=%v err=%v child=%v", c != nil, err, cp.child != nil)
	}
}

func TestLeaseReviewApproveLostReply(t *testing.T) {
	leaseReviewHome(t)
	path := filepath.Join(leaseReviewCtlDir(t), "missing-approve.sock")
	exited := make(chan struct{})
	out := &bytes.Buffer{}
	cp := &coldPair{out: out, child: &leaseChild{proc: &os.Process{Pid: os.Getpid()}, exited: exited, path: path}}
	sel := selection{path: path, pid: os.Getpid(), p: lease.Pending{Request: "r1", TTL: 300, JKT: strings.Repeat("A", 43)}}
	if err := cp.approve(sel); err != nil {
		t.Fatal(err)
	}
	if !cp.kept || !strings.Contains(out.String(), "unknown") || !strings.Contains(out.String(), "lease status") || strings.Contains(out.String(), "nothing was issued") || strings.Contains(out.String(), "lease list") {
		t.Fatalf("transport lost reply: kept=%v out=%q", cp.kept, out.String())
	}
}

func TestLeaseReviewApproveStaleReject(t *testing.T) {
	leaseReviewHome(t)
	h := ctl.NewHolder("follow", "fake")
	h.Answer(ctl.OpLeaseApprove, func(ctl.Request) map[string]any {
		return map[string]any{"ok": false, "error": "stale request"}
	})
	exited := make(chan struct{})
	srv, err := ctl.Serve(h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	out := &bytes.Buffer{}
	cp := &coldPair{out: out, child: &leaseChild{proc: &os.Process{Pid: os.Getpid()}, exited: exited, path: srv.Path()}}
	sel := selection{path: srv.Path(), pid: os.Getpid(), p: lease.Pending{Request: "r1", TTL: 300, JKT: strings.Repeat("A", 43)}}
	err = cp.approve(sel)
	if err == nil || !errors.Is(err, errDecisionRefused) || cp.kept {
		t.Fatalf("stale reject: kept=%v err=%v out=%q", cp.kept, err, out.String())
	}
	close(exited)
	if err := cp.afterDecision("APPROVE", err); err == nil || !strings.Contains(err.Error(), "nothing was issued") {
		t.Fatalf("stale reject afterDecision: %v", err)
	}
	if cp.child != nil && cp.child.alive() {
		t.Fatal("stale reject left a live review child")
	}
}

func TestLeaseReviewApproveTTL(t *testing.T) {
	leaseReviewHome(t)
	h := ctl.NewHolder("follow", "fake")
	h.Answer(ctl.OpLeaseApprove, func(ctl.Request) map[string]any {
		return map[string]any{"ok": true}
	})
	exited := make(chan struct{})
	srv, err := ctl.Serve(h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	out := &bytes.Buffer{}
	cp := &coldPair{out: out, child: &leaseChild{proc: &os.Process{Pid: os.Getpid()}, exited: exited, path: srv.Path()}}
	sel := selection{path: srv.Path(), pid: os.Getpid(), p: lease.Pending{Request: "r1", TTL: 300, JKT: strings.Repeat("A", 43)}}
	if err := cp.approve(sel); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Approved for 5m0s from approval") {
		t.Fatalf("ok approval: %q", out.String())
	}
}

func TestLeaseReviewDenyFlush(t *testing.T) {
	cp := &coldPair{out: &bytes.Buffer{}}
	start := time.Now()
	if err := cp.afterDecision("DENY", nil); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < leaseDecisionFlush {
		t.Fatalf("deny returned after %s without the grace", took)
	}
	if !strings.Contains(cp.out.(*bytes.Buffer).String(), "nothing was issued") {
		t.Fatal("deny transcript missing")
	}
}

func TestLeaseReviewDeviceBusyBound(t *testing.T) {
	leaseReviewHome(t)
	ctlDir := leaseReviewCtlDir(t)
	const hang = `import os,socket,sys,time
d=sys.argv[1]
p=os.path.join(d,str(os.getpid())+".sock")
s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
s.bind(p); s.listen(1)
c,_=s.accept(); time.sleep(60)
`
	for i := 0; i < 4; i++ {
		cmd := exec.Command("python3", "-c", hang, ctlDir)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(filepath.Join(ctlDir, strconv.Itoa(cmd.Process.Pid)+".sock")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("hanging peer socket did not appear")
			}
		}
	}
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- leaseDeviceBusy("ab", os.Getpid()) }()
	select {
	case <-done:
		if took := time.Since(start); took > 4*time.Second {
			t.Fatalf("leaseDeviceBusy took %s without a bound", took)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("leaseDeviceBusy hung on foreign sockets")
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
