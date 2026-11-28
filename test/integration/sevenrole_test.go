package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
)

var sevenRolePackages = map[string]string{
	"internal/protect":         "protect.go",
	"internal/browser":         "browser.go",
	"internal/signal":          "signal.go",
	"internal/signal/loopback": "loopback.go",
	"internal/flow":            "flow.go",
}

type sevenRoleEdit struct {
	file, anchor, to, tail string
}

func sevenRoleSevenFunc(prefix string) string {
	widen := ""
	if prefix != "" {
		widen = "\ts.ExcludedLabelPrefixes = append(append([]string(nil), s.ExcludedLabelPrefixes...), \"" + prefix + "\")\n"
	}
	return "\nfunc sevenRoleSeven(s sliccbrowsers.Seven) sliccbrowsers.Seven {\n\tvar labels []string\n\tfor _, l := range s.ExcludedLabels {\n\t\tif l != \"lease-pilot\" {\n\t\t\tlabels = append(labels, l)\n\t\t}\n\t}\n\ts.ExcludedLabels = labels\n" + widen + "\treturn s\n}\n"
}

func sevenRoleProtectCatalog(prefix string) sevenRoleEdit {
	return sevenRoleEdit{
		file:   "internal/protect/protect.go",
		anchor: "seven = sliccbrowsers.Default().Seven",
		to:     "seven = sevenRoleSeven(sliccbrowsers.Default().Seven)",
		tail:   sevenRoleSevenFunc(prefix),
	}
}

func sevenRoleBrowserCatalog(prefix string) sevenRoleEdit {
	return sevenRoleEdit{
		file:   "internal/browser/table.go",
		anchor: "var catalog = sliccbrowsers.Default()",
		to:     "var catalog = func(c sliccbrowsers.Catalog) *sliccbrowsers.Catalog { c.Seven = sevenRoleSeven(c.Seven); return &c }(*sliccbrowsers.Default())",
		tail:   sevenRoleSevenFunc(prefix),
	}
}

var sevenRoleForbiddenGainsCORS = sevenRoleEdit{
	file:   "internal/signal/loopback/listener.go",
	anchor: "l.cfg.Logf(\"loopback: refused %s %s: origin\", r.Method, r.RequestURI)",
	to:     "l.cfg.Logf(\"loopback: refused %s %s: origin\", r.Method, r.RequestURI)\n\t\tw.Header().Set(\"Access-Control-Allow-Methods\", \"POST\")",
}

func sevenRoleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func sevenRoleEnv(t *testing.T) []string {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	gotmp := filepath.Join(tmp, "gotmp")
	for _, d := range []string{home, gotmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	goflags := "-mod=readonly"
	if sevenRoleRace {
		goflags += " -race"
	}
	env := []string{
		"GOFLAGS=" + goflags,
		"GOPROXY=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"GOENV=off",
		"CGO_ENABLED=" + sevenRoleCgo,
		"GOTMPDIR=" + gotmp,
		"TMPDIR=" + gotmp,
		"TMP=" + gotmp,
		"TEMP=" + gotmp,
		"HOME=" + home,
		"USERPROFILE=" + home,
		"APPDATA=" + filepath.Join(home, "Roaming"),
		"LOCALAPPDATA=" + filepath.Join(home, "Local"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
	}
	for _, key := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		v := os.Getenv(key)
		if v == "" {
			t.Fatalf("%s is not set; the integration TestMain exports it before the nested go test", key)
		}
		env = append(env, key+"="+v)
	}
	keep := []string{"PATH", "SystemRoot", "SYSTEMROOT", "PATHEXT", "ComSpec", "WINDIR"}
	if sevenRoleCgo == "1" {
		keep = append(keep, "CC", "CXX")
	}
	for _, key := range keep {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	return env
}

func sevenRoleRun(t *testing.T, edits []sevenRoleEdit) string {
	t.Helper()
	root := sevenRoleRoot(t)
	scratch := t.TempDir()
	replace := map[string]string{}
	var pkgs []string
	for dir, src := range sevenRolePackages {
		replace[filepath.Join(root, dir, "sevenrole_test.go")] = filepath.Join(root, "test/integration/testdata/sevenrole", src)
		pkgs = append(pkgs, "./"+dir)
	}
	slices.Sort(pkgs)
	mutated := map[string]string{}
	for _, e := range edits {
		text, ok := mutated[e.file]
		if !ok {
			data, err := os.ReadFile(filepath.Join(root, e.file))
			if err != nil {
				t.Fatal(err)
			}
			text = string(data)
		}
		if n := strings.Count(text, e.anchor); n != 1 {
			t.Fatalf("mutant anchor %q appears %d times in %s", e.anchor, n, e.file)
		}
		mutated[e.file] = strings.Replace(text, e.anchor, e.to, 1) + e.tail
	}
	for file, body := range mutated {
		dest := filepath.Join(scratch, strings.ReplaceAll(file, "/", "_"))
		if err := os.WriteFile(dest, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		replace[filepath.Join(root, file)] = dest
	}
	data, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(scratch, "overlay.json")
	if err := os.WriteFile(overlay, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", append([]string{"test", "-count=1", "-overlay", overlay, "-v", "-run", "^TestSevenRole"}, pkgs...)...)
	cmd.Dir = root
	cmd.Env = sevenRoleEnv(t)
	out, err := cmd.CombinedOutput()
	for dir := range sevenRolePackages {
		if !regexp.MustCompile(`(?m)^(ok|FAIL|---)?\s*github\.com/ai-ecoverse/slicc-cli/` + regexp.QuoteMeta(dir) + `\s`).Match(out) {
			t.Fatalf("go test with the overlay did not report %s: %v\n%s", dir, err, out)
		}
	}
	if len(edits) == 0 && err != nil {
		t.Fatalf("seven-role rows: %v\n%s", err, out)
	}
	if len(edits) > 0 && err == nil {
		t.Fatalf("mutant passed\n%s", out)
	}
	return string(out)
}

var sevenRoleResult = regexp.MustCompile(`(?m)^\s*--- (PASS|FAIL): (TestSevenRole\S+) \(`)

func sevenRoleLeaves(out string) map[string]string {
	all := map[string]string{}
	for _, m := range sevenRoleResult.FindAllStringSubmatch(out, -1) {
		all[m[2]] = m[1]
	}
	leaves := map[string]string{}
	for name, result := range all {
		parent := false
		for other := range all {
			if strings.HasPrefix(other, name+"/") {
				parent = true
				break
			}
		}
		if !parent {
			leaves[name] = result
		}
	}
	return leaves
}

var sevenRoleManifest = []string{
	"TestSevenRolePilotHost/Exact/Protected",
	"TestSevenRolePilotHost/Exact/OrdinarySeven",
	"TestSevenRolePilotHost/Upper/Protected",
	"TestSevenRolePilotHost/Upper/OrdinarySeven",
	"TestSevenRolePilotHost/TrailingDot/Protected",
	"TestSevenRolePilotHost/TrailingDot/OrdinarySeven",
	"TestSevenRolePilotHost/Port443/Protected",
	"TestSevenRolePilotHost/Port443/OrdinarySeven",
	"TestSevenRolePilotHost/SubDomain/Protected",
	"TestSevenRolePilotHost/SubDomain/OrdinarySeven",
	"TestSevenRolePilotHost/Port8443/Protected",
	"TestSevenRolePilotHost/Port8443/OrdinarySeven",
	"TestSevenRolePilotHost/HTTP/Protected",
	"TestSevenRolePilotHost/HTTP/OrdinarySeven",
	"TestSevenRoleControls/seven",
	"TestSevenRoleControls/www",
	"TestSevenRoleControls/lease",
	"TestSevenRoleControls/lease-pilot-x",
	"TestSevenRoleControls/x-lease-pilot",
	"TestSevenRoleControls/vault",
	"TestSevenRoleControls/vault-preview",
	"TestSevenRoleControls/vault-main",
	"TestSevenRoleControls/echo",
	"TestSevenRoleTabSelection/PilotAndSeven",
	"TestSevenRoleTabSelection/PilotOnly",
	"TestSevenRoleTabSelection/SevenOnly",
	"TestSevenRoleAttachCandidates/PilotAndSeven",
	"TestSevenRoleAttachCandidates/PilotOnly",
	"TestSevenRoleAttachCandidates/PilotOrigin",
	"TestSevenRoleAttachCandidates/SevenOrigin",
	"TestSevenRolePairingGate/PilotRefused",
	"TestSevenRolePairingGate/PilotPreflightRefused",
	"TestSevenRolePairingGate/SevenPreflight",
	"TestSevenRolePairingGate/SevenPairs",
	"TestSevenRolePageFlag/Pilot",
	"TestSevenRolePageFlag/PilotUpper",
	"TestSevenRolePageFlag/Pilot443",
	"TestSevenRolePageFlag/Seven",
	"TestSevenRolePageFlag/Default",
	"TestSevenRolePageFlag/Six",
	"TestSevenRolePilotSevenOrVaultHost/Exact",
	"TestSevenRolePilotSevenOrVaultHost/Upper",
	"TestSevenRolePilotSevenOrVaultHost/TrailingDot",
	"TestSevenRolePilotSevenOrVaultHost/Port443",
	"TestSevenRolePilotSevenOrVaultHost/SubDomain",
	"TestSevenRolePilotSevenOrVaultHost/Port8443",
	"TestSevenRolePilotSevenOrVaultHost/HTTP",
	"TestSevenRolePilotKeptToday/Root",
	"TestSevenRolePilotKeptToday/OS",
	"TestSevenRoleProtectControls/seven",
	"TestSevenRoleProtectControls/www",
	"TestSevenRoleProtectControls/lease",
	"TestSevenRoleProtectControls/lease-pilot-x",
	"TestSevenRoleProtectControls/x-lease-pilot",
	"TestSevenRoleProtectControls/vault",
	"TestSevenRoleProtectControls/vault-preview",
	"TestSevenRoleProtectControls/vault-main",
}

var (
	sevenRoleBrowserRed = []string{
		"TestSevenRolePilotHost/Exact/OrdinarySeven",
		"TestSevenRolePilotHost/Upper/OrdinarySeven",
		"TestSevenRolePilotHost/Port443/OrdinarySeven",
		"TestSevenRoleTabSelection/PilotAndSeven",
		"TestSevenRoleTabSelection/PilotOnly",
		"TestSevenRoleAttachCandidates/PilotAndSeven",
		"TestSevenRoleAttachCandidates/PilotOnly",
		"TestSevenRoleAttachCandidates/PilotOrigin",
		"TestSevenRolePairingGate/PilotRefused",
		"TestSevenRolePairingGate/PilotPreflightRefused",
		"TestSevenRolePageFlag/Pilot",
		"TestSevenRolePageFlag/PilotUpper",
		"TestSevenRolePageFlag/Pilot443",
	}
	sevenRoleProtectRed = []string{
		"TestSevenRolePilotSevenOrVaultHost/Exact",
		"TestSevenRolePilotSevenOrVaultHost/Upper",
		"TestSevenRolePilotSevenOrVaultHost/TrailingDot",
		"TestSevenRolePilotSevenOrVaultHost/Port443",
		"TestSevenRolePilotKeptToday/OS",
	}
	sevenRoleLookalikeRed = []string{
		"TestSevenRoleControls/lease-pilot-x",
		"TestSevenRoleProtectControls/lease-pilot-x",
	}
	sevenRoleLeaseRed = []string{
		"TestSevenRoleControls/lease",
		"TestSevenRoleProtectControls/lease",
	}
)

func sevenRoleBaseline(t *testing.T) map[string]string {
	t.Helper()
	leaves := sevenRoleLeaves(sevenRoleRun(t, nil))
	for _, row := range sevenRoleManifest {
		if leaves[row] != "PASS" {
			t.Errorf("row %s: %q, want PASS", row, leaves[row])
		}
	}
	for row := range leaves {
		if !slices.Contains(sevenRoleManifest, row) {
			t.Errorf("row %s ran but is not in the manifest", row)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	return leaves
}

func TestSevenRoleCatalogPin(t *testing.T) {
	s := sliccbrowsers.Default().Seven
	if n := strings.Count(","+strings.Join(s.ExcludedLabels, ",")+",", ",lease-pilot,"); n != 1 {
		t.Fatalf("seven.excludedLabels %q names lease-pilot %d times, want once", s.ExcludedLabels, n)
	}
	for label, want := range map[string]bool{"lease-pilot": false, "lease-pilot-x": true, "x-lease-pilot": true, "lease": true, "seven": true, "www": true, "vault": false} {
		if got := s.IsSevenLabel(label); got != want {
			t.Errorf("IsSevenLabel(%q) = %v, want %v", label, got, want)
		}
	}
}

func TestSevenRoleRows(t *testing.T) {
	sevenRoleBaseline(t)
}

func TestSevenRoleCatchesMutants(t *testing.T) {
	baseline := sevenRoleBaseline(t)
	for _, m := range []struct {
		name  string
		edits []sevenRoleEdit
		red   []string
	}{
		{"CatalogWithoutExclusion", []sevenRoleEdit{sevenRoleProtectCatalog(""), sevenRoleBrowserCatalog("")}, slices.Concat(sevenRoleBrowserRed, sevenRoleProtectRed)},
		{"ProtectCatalogWithoutExclusion", []sevenRoleEdit{sevenRoleProtectCatalog("")}, sevenRoleProtectRed},
		{"BrowserCatalogWithoutExclusion", []sevenRoleEdit{sevenRoleBrowserCatalog("")}, sevenRoleBrowserRed},
		{"ExclusionAsPrefix", []sevenRoleEdit{sevenRoleProtectCatalog("lease-pilot"), sevenRoleBrowserCatalog("lease-pilot")}, sevenRoleLookalikeRed},
		{"ExclusionTooBroad", []sevenRoleEdit{sevenRoleProtectCatalog("lease"), sevenRoleBrowserCatalog("lease")}, slices.Concat(sevenRoleLookalikeRed, sevenRoleLeaseRed)},
		{"PilotForbiddenGainsCORS", []sevenRoleEdit{sevenRoleForbiddenGainsCORS}, []string{"TestSevenRolePairingGate/PilotRefused", "TestSevenRolePairingGate/PilotPreflightRefused"}},
	} {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			out := sevenRoleRun(t, m.edits)
			got := sevenRoleLeaves(out)
			for row := range baseline {
				want := "PASS"
				if slices.Contains(m.red, row) {
					want = "FAIL"
				}
				if got[row] != want {
					t.Errorf("mutant %s: row %s %q, want %s", m.name, row, got[row], want)
				}
			}
			for _, row := range m.red {
				if _, ok := baseline[row]; !ok {
					t.Errorf("mutant %s names red row %s, which is not in the baseline", m.name, row)
				}
			}
			for row, result := range got {
				if _, ok := baseline[row]; !ok {
					t.Errorf("mutant %s: row %s %s is not in the baseline", m.name, row, result)
				}
			}
			if t.Failed() {
				t.Log(out)
			}
		})
	}
}
