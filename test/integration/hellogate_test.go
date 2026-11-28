package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	helloGateBinaryEnv = "SLICC_TEST_HELLOGATE_BINARY"
	helloGateCacheEnv  = "SLICC_TEST_HELLOGATE_GOCACHE"
	helloGateToyEnv    = "SLICC_TEST_HELLOGATE_TOY"
	helloGatePhaseMark = "hellogate-phase-unreached"
)

type helloGateEdit struct {
	file, anchor, to string
}

type helloGateMutant struct {
	name, pkg, fixture, dest, run string
	edits                         []helloGateEdit
	red                           []string
}

type helloGateFollowerMutant struct {
	name  string
	edits []helloGateEdit
	red   []string
}

const (
	helloGateLinkFixture   = "test/integration/testdata/hellogate/link.go"
	helloGateLinkDest      = "internal/link/hellogate_test.go"
	helloGateFollowFixture = "test/integration/testdata/hellogate/follow.go"
	helloGateFollowDest    = "cmd/slicc/hellogate_test.go"

	helloGateGateCond       = "\tif l.opts.PeerGate != nil && !l.opts.PeerGate(l.pc) {\n"
	helloGateChannelGate    = helloGateGateCond + "\t\tl.fail(ErrPeerRefused)\n\t\t_ = dc.Close()\n\t\treturn\n\t}\n"
	helloGatePageRole       = "\tif opts.PeerGate != nil && role == RolePage {\n\t\treturn nil, errors.New(\"link: PeerGate is not supported for the page role\")\n\t}\n"
	helloGateControlCase    = "\tcase ControlName:\n\t\tl.mu.Lock()\n\t\tdup := l.control != nil\n\t\tl.mu.Unlock()\n\t\tif dup {\n\t\t\tl.violation(\"a second control channel\")\n\t\t\treturn\n\t\t}\n\t\tl.setControl(dc)\n"
	helloGatePeerGateAssign = ", PeerGate: func(pc *webrtc.PeerConnection) bool { return pinMatches(pc, pin) }"
	helloGatePinMatchBody   = "\tder := pc.SCTP().Transport().GetRemoteCertificate()\n\tif len(der) == 0 {\n\t\treturn false\n\t}\n\th := sha256.Sum256(der)\n\treturn hex.EncodeToString(h[:]) == pin"
	helloGateAnyLeafBody    = "\tder := pc.SCTP().Transport().GetRemoteCertificate()\n\tsum := sha256.Sum256(der)\n\t_ = hex.EncodeToString(sum[:])\n\t_ = pin\n\treturn len(der) > 0"
	helloGatePostReady      = "\t\t\t\t\tif !peerPinned(ev.Link, pin) {\n\t\t\t\t\t\t_ = ev.Link.Close()\n"
	helloGatePostReadyOff   = "\t\t\t\t\tif false && !peerPinned(ev.Link, pin) {\n\t\t\t\t\t\t_ = ev.Link.Close()\n"
	helloGateStreamAuth     = "\t\tif !peerPinned(s.Link(), pin) {\n\t\t\t_ = s.Reset(\"E_AUTH\")\n\t\t\treturn\n\t\t}\n"
	helloGateEmptyLeaf      = "\tif len(der) == 0 {\n\t\treturn false\n\t}\n\th := sha256.Sum256(der)\n"
	helloGateFailMapping    = "\tif errors.Is(err, link.ErrPeerRefused) {\n\t\treturn \"paired fingerprint mismatch\"\n\t}\n"
)

var helloGatePostReadyVariant = []helloGateEdit{
	{file: "cmd/slicc/leasefollow.go", anchor: helloGatePeerGateAssign, to: ""},
}

var helloGateStreamAuthVariant = []helloGateEdit{
	{file: "cmd/slicc/leasefollow.go", anchor: helloGatePeerGateAssign, to: ", PeerGate: func(pc *webrtc.PeerConnection) bool { return true }"},
	{file: "cmd/slicc/leasefollow.go", anchor: helloGatePostReady, to: helloGatePostReadyOff},
}

var helloGateLinkManifest = []string{
	"TestP3MismatchSendsNothing",
	"TestP3MismatchParsesNothing",
	"TestP3MismatchStreamChannelFirst",
	"TestP3MatchUnchanged",
	"TestP3OrdinaryFollowUnchanged",
	"TestP3OrdinaryFailedChannelStillProcessed",
	"TestP3PageRoleGateRefused",
	"TestP3RelinkAcceptsLegitimateAfterRefused",
	"TestP3PostReadyCheckStillHolds",
	"TestP3StreamAuthStillHolds",
	"TestP3DefenceInDepth",
}

var helloGateFollowManifest = []string{
	"TestP3RefusalText",
	"TestP3NodeForwardsPeerGate",
	"TestP3EmptyLeafFailClosed",
	"TestP3ErrorMappingUsesPeerRefused",
}

func helloGateMutants() []helloGateMutant {
	link := func(name, run string, red []string, edits ...helloGateEdit) helloGateMutant {
		return helloGateMutant{name: name, pkg: "./internal/link", fixture: helloGateLinkFixture, dest: helloGateLinkDest, run: run, edits: edits, red: red}
	}
	follow := func(name, run string, red []string, edits ...helloGateEdit) helloGateMutant {
		return helloGateMutant{name: name, pkg: "./cmd/slicc", fixture: helloGateFollowFixture, dest: helloGateFollowDest, run: run, edits: edits, red: red}
	}
	const all = "^TestP3"
	mismatch := []string{"TestP3MismatchSendsNothing", "TestP3MismatchParsesNothing", "TestP3MismatchStreamChannelFirst", "TestP3RelinkAcceptsLegitimateAfterRefused"}
	return []helloGateMutant{
		link("ChannelGateRemoved", all, mismatch,
			helloGateEdit{file: "internal/link/link.go", anchor: helloGateChannelGate, to: ""}),
		link("GateRemoved", all, mismatch,
			helloGateEdit{file: "internal/link/link.go", anchor: helloGateGateCond, to: "\tif l.opts.PeerGate != nil && false {\n"}),
		link("GateAfterSetControl", "^TestP3MismatchSendsNothing$", []string{"TestP3MismatchSendsNothing"},
			helloGateEdit{file: "internal/link/link.go", anchor: helloGateChannelGate, to: ""},
			helloGateEdit{file: "internal/link/link.go", anchor: "\tdc.OnOpen(func() { ch.sendOnce.Do(func() { l.sendHello(ch) }) })\n", to: "\tdc.OnOpen(func() {\n\t\tch.sendOnce.Do(func() { l.sendHello(ch) })\n\t\tif l.opts.PeerGate != nil && !l.opts.PeerGate(l.pc) {\n\t\t\tl.fail(ErrPeerRefused)\n\t\t\t_ = dc.Close()\n\t\t\treturn\n\t\t}\n\t})\n"}),
		link("GateOnControlLabelOnly", all, []string{"TestP3MismatchStreamChannelFirst"},
			helloGateEdit{file: "internal/link/link.go", anchor: helloGateChannelGate, to: ""},
			helloGateEdit{file: "internal/link/link.go", anchor: helloGateControlCase, to: "\tcase ControlName:\n\t\tif l.opts.PeerGate != nil && !l.opts.PeerGate(l.pc) {\n\t\t\tl.fail(ErrPeerRefused)\n\t\t\t_ = dc.Close()\n\t\t\treturn\n\t\t}\n" + strings.TrimPrefix(helloGateControlCase, "\tcase ControlName:\n")}),
		link("GateInverted", "^TestP3MatchUnchanged$|^TestP3MismatchSendsNothing$", []string{"TestP3MatchUnchanged", "TestP3MismatchSendsNothing"},
			helloGateEdit{file: "internal/link/link.go", anchor: helloGateGateCond, to: "\tif l.opts.PeerGate != nil && l.opts.PeerGate(l.pc) {\n"}),
		link("GateAcceptsAnyLeaf", "^TestP3MismatchSendsNothing$|^TestP3MismatchParsesNothing$", []string{"TestP3MismatchSendsNothing", "TestP3MismatchParsesNothing"},
			helloGateEdit{file: helloGateLinkFixture, anchor: helloGatePinMatchBody, to: helloGateAnyLeafBody}),
		link("FailNotCalled", all, mismatch,
			helloGateEdit{file: "internal/link/link.go", anchor: "\t\tl.fail(ErrPeerRefused)\n\t\t_ = dc.Close()\n", to: "\t\t_ = l.pc.Close()\n\t\t_ = dc.Close()\n"}),
		link("PageRoleAccepted", all, []string{"TestP3PageRoleGateRefused"},
			helloGateEdit{file: "internal/link/link.go", anchor: helloGatePageRole, to: ""}),
		link("GateAppliedToAllLinks", "^TestP3OrdinaryFollowUnchanged$", []string{"TestP3OrdinaryFollowUnchanged"},
			helloGateEdit{file: "internal/link/link.go", anchor: helloGatePageRole, to: "\tif opts.PeerGate == nil {\n\t\topts.PeerGate = func(*webrtc.PeerConnection) bool { return false }\n\t}\n" + helloGatePageRole}),
		link("OrdinaryFailedChannelClosed", "^TestP3OrdinaryFailedChannelStillProcessed$", []string{"TestP3OrdinaryFailedChannelStillProcessed"},
			helloGateEdit{file: "internal/link/link.go", anchor: helloGateGateCond, to: "\tif l.Err() != nil || (l.opts.PeerGate != nil && !l.opts.PeerGate(l.pc)) {\n"}),
		link("RelinkGateSkipped", "^TestP3RelinkAcceptsLegitimateAfterRefused$", []string{"TestP3RelinkAcceptsLegitimateAfterRefused"},
			helloGateEdit{file: "internal/link/link.go", anchor: "type Link struct {\n\tpc     *webrtc.PeerConnection\n", to: "var helloGateRelinkOnce sync.Once\nvar helloGateRelinkOK bool\n\nfunc helloGateRelinkCached(l *Link) bool {\n\thelloGateRelinkOnce.Do(func() { helloGateRelinkOK = l.opts.PeerGate(l.pc) })\n\treturn helloGateRelinkOK\n}\n\ntype Link struct {\n\tpc     *webrtc.PeerConnection\n"},
			helloGateEdit{file: "internal/link/link.go", anchor: helloGateGateCond, to: "\tif l.opts.PeerGate != nil && !helloGateRelinkCached(l) {\n"}),
		link("PostReadyCheckRemoved", "^TestP3PostReadyCheckStillHolds$|^TestP3DefenceInDepth$", []string{"TestP3PostReadyCheckStillHolds", "TestP3DefenceInDepth"},
			helloGateEdit{file: helloGateLinkFixture, anchor: "func postReadyPinHolds(node *Link, pin string) bool {\n\treturn !pinMatchesPC(node.PeerConnection(), pin)\n}", to: "func postReadyPinHolds(node *Link, pin string) bool {\n\treturn false\n}"}),
		link("StreamAuthRemoved", "^TestP3StreamAuthStillHolds$|^TestP3DefenceInDepth$", []string{"TestP3StreamAuthStillHolds", "TestP3DefenceInDepth"},
			helloGateEdit{file: helloGateLinkFixture, anchor: "\t\tif !pinMatchesPC(s.Link().PeerConnection(), pin) {\n\t\t\t_ = s.Reset(\"E_AUTH\")\n\t\t\tgot <- \"E_AUTH\"\n\t\t\treturn\n\t\t}\n\t\tgot <- \"ok\"\n", to: "\t\tgot <- \"ok\"\n"}),
		follow("ErrorMappingRemoved", all, []string{"TestP3RefusalText", "TestP3ErrorMappingUsesPeerRefused"},
			helloGateEdit{file: "cmd/slicc/leasefollow.go", anchor: helloGateFailMapping, to: ""}),
		follow("EmptyLeafAccepted", "^TestP3EmptyLeafFailClosed$", []string{"TestP3EmptyLeafFailClosed"},
			helloGateEdit{file: "cmd/slicc/leasefollow.go", anchor: helloGateEmptyLeaf, to: "\th := sha256.Sum256(der)\n"}),
	}
}

func helloGateRoot(t *testing.T) string {
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

func helloGateApply(root string, edits []helloGateEdit) (map[string]string, error) {
	mutated := map[string]string{}
	for i, e := range edits {
		text, ok := mutated[e.file]
		if !ok {
			data, err := os.ReadFile(filepath.Join(root, e.file))
			if err != nil {
				return nil, err
			}
			text = string(data)
		}
		if n := strings.Count(text, e.anchor); n != 1 {
			return nil, fmt.Errorf("edit %d in %s: anchor appears %d times (want 1): %q", i+1, e.file, n, e.anchor)
		}
		mutated[e.file] = strings.Replace(text, e.anchor, e.to, 1)
	}
	return mutated, nil
}

func helloGateAnchorProblems(root string) (problems []string, counted int) {
	check := func(name string, edits []helloGateEdit) {
		if len(edits) == 0 {
			problems = append(problems, name+": no edits")
			return
		}
		if _, err := helloGateApply(root, edits); err != nil {
			problems = append(problems, name+": "+err.Error())
		}
		counted += len(edits)
	}
	for _, m := range helloGateMutants() {
		check("mutant "+m.name, m.edits)
	}
	for _, m := range helloGateFollowerMutants() {
		check("follower mutant "+m.name, m.edits)
	}
	check("follower variant PostReadyCheckStillHolds", helloGatePostReadyVariant)
	check("follower variant StreamAuthStillHolds", helloGateStreamAuthVariant)
	return problems, counted
}

func helloGateRequireAnchors(t *testing.T) {
	t.Helper()
	problems, counted := helloGateAnchorProblems(helloGateRoot(t))
	if len(problems) > 0 {
		t.Fatalf("stale mutant anchors:\n%s", strings.Join(problems, "\n"))
	}
	t.Logf("anchors counted exactly once: %d edits", counted)
}

func TestHelloGateMutantAnchors(t *testing.T) {
	helloGateRequireAnchors(t)
}

var helloGateCache struct {
	once sync.Once
	dir  string
	err  error
}

func helloGateChildCache(t *testing.T) string {
	t.Helper()
	helloGateCache.once.Do(func() {
		dir := os.Getenv(helloGateCacheEnv)
		if dir == "" {
			if binary == "" {
				helloGateCache.err = fmt.Errorf("the integration TestMain binary directory is not set")
				return
			}
			dir = filepath.Join(filepath.Dir(binary), "hellogate-gocache")
		}
		helloGateCache.dir, helloGateCache.err = dir, os.MkdirAll(dir, 0o700)
	})
	if helloGateCache.err != nil {
		t.Fatal(helloGateCache.err)
	}
	return helloGateCache.dir
}

func helloGateScrubRace(goflags string) string {
	var out []string
	for _, f := range strings.Fields(goflags) {
		if f == "-race" || strings.HasPrefix(f, "-race=") {
			continue
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

func helloGateGoEnv(t *testing.T, key string) string {
	t.Helper()
	if v := os.Getenv(key); v != "" {
		return v
	}
	out, err := exec.Command("go", "env", key).Output()
	if err != nil {
		t.Fatalf("go env %s: %v", key, err)
	}
	return strings.TrimSpace(string(out))
}

func helloGateEnvMode(t *testing.T, race bool) []string {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	gotmp := filepath.Join(tmp, "gotmp")
	for _, d := range []string{home, gotmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	goflags := helloGateScrubRace(helloGateGoEnv(t, "GOFLAGS"))
	if !strings.Contains(goflags, "-mod=") {
		goflags = strings.TrimSpace(goflags + " -mod=readonly")
	}
	cgo := "0"
	if race && helloGateRace && helloGateCgo == "1" {
		cgo = "1"
		goflags += " -race"
	}
	cache := helloGateChildCache(t)
	env := []string{
		"GOFLAGS=" + goflags,
		"GOPROXY=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"GOENV=off",
		"CGO_ENABLED=" + cgo,
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
		"GOCACHE=" + cache,
		helloGateCacheEnv + "=" + cache,
	}
	for _, key := range []string{"GOMODCACHE", "GOPATH"} {
		v := helloGateGoEnv(t, key)
		if v == "" {
			t.Fatalf("%s is empty; the nested go command cannot find the module cache", key)
		}
		env = append(env, key+"="+v)
	}
	keep := []string{"PATH", "SystemRoot", "SYSTEMROOT", "PATHEXT", "ComSpec", "WINDIR"}
	if cgo == "1" {
		keep = append(keep, "CC", "CXX")
	}
	for _, key := range keep {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	return env
}

func helloGateOverlay(t *testing.T, root string, m helloGateMutant) string {
	t.Helper()
	scratch := t.TempDir()
	replace := map[string]string{filepath.Join(root, m.dest): filepath.Join(root, m.fixture)}
	mutated, err := helloGateApply(root, m.edits)
	if err != nil {
		t.Fatalf("mutant %s: %v", m.name, err)
	}
	for file, body := range mutated {
		dest := filepath.Join(scratch, strings.ReplaceAll(file, "/", "_"))
		if err := os.WriteFile(dest, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if file == m.fixture {
			replace[filepath.Join(root, m.dest)] = dest
			continue
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
	return overlay
}

func helloGateRun(t *testing.T, m helloGateMutant) (string, error) {
	t.Helper()
	root := helloGateRoot(t)
	overlay := helloGateOverlay(t, root, m)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-v", "-run", m.run, m.pkg)
	cmd.Dir = root
	cmd.Env = helloGateEnvMode(t, true)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func helloGateExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode()
	}
	return -1
}

var helloGateResult = regexp.MustCompile(`(?m)^\s*--- (PASS|FAIL|SKIP): (TestP3\S+) \(`)

func helloGateLeaves(out string) map[string]string {
	all := map[string]string{}
	for _, m := range helloGateResult.FindAllStringSubmatch(out, -1) {
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

func helloGateClassify(row, out string, err error) string {
	leaf := helloGateLeaves(out)[row]
	code := helloGateExitCode(err)
	switch {
	case strings.Contains(out, helloGatePhaseMark):
		return "unclean"
	case leaf == "FAIL" && code == 1:
		return "killed"
	case leaf == "PASS" && code == 0:
		return "pass"
	case leaf == "SKIP":
		return "skip"
	default:
		return "unclean"
	}
}

func helloGateRunChild(t *testing.T, row string, env []string, testTimeout time.Duration) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout+5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.count=1", "-test.v", "-test.timeout="+testTimeout.String(), "-test.run", "^"+regexp.QuoteMeta(row)+"$")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func helloGateTail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

func TestP3HelloGateDriverToy(t *testing.T) {
	switch mode := os.Getenv(helloGateToyEnv); mode {
	case "", "pass":
	case "fail":
		t.Fatal("toy failure")
	case "crash":
		os.Exit(1)
	case "timeout":
		time.Sleep(time.Hour)
	case "skip":
		t.Skip("toy skip")
	case "phase":
		t.Fatal(helloGatePhaseMark + ": toy never reached the handshake")
	default:
		t.Fatalf("unknown %s %q", helloGateToyEnv, mode)
	}
}

func helloGateSelfTest(t *testing.T) {
	t.Helper()
	cases := []struct {
		mode, want string
		timeout    time.Duration
	}{
		{"pass", "pass", 2 * time.Minute},
		{"fail", "killed", 2 * time.Minute},
		{"crash", "unclean", 2 * time.Minute},
		{"timeout", "unclean", 5 * time.Second},
		{"skip", "skip", 2 * time.Minute},
		{"phase", "unclean", 2 * time.Minute},
	}
	for _, tc := range cases {
		env := append(helloGateEnvMode(t, false), helloGateToyEnv+"="+tc.mode)
		out, err := helloGateRunChild(t, "TestP3HelloGateDriverToy", env, tc.timeout)
		if got := helloGateClassify("TestP3HelloGateDriverToy", out, err); got != tc.want {
			t.Fatalf("driver self-test: toy %s classified %q, want %q (exit %d)\n%s", tc.mode, got, tc.want, helloGateExitCode(err), helloGateTail(out, 30))
		}
	}
	t.Logf("driver self-test: toy pass/fail/crash/timeout/skip/phase classified pass/killed/unclean/unclean/skip/unclean")
}

func helloGateRequirePass(t *testing.T, what string, leaves map[string]string, manifest []string, out string, err error) {
	t.Helper()
	if code := helloGateExitCode(err); code != 0 {
		t.Errorf("%s baseline exited %d", what, code)
	}
	for _, row := range manifest {
		if leaves[row] != "PASS" {
			t.Errorf("%s baseline row %s: %q, want PASS", what, row, leaves[row])
		}
	}
	for row := range leaves {
		if !slices.Contains(manifest, row) {
			t.Errorf("%s baseline row %s ran but is not in the manifest", what, row)
		}
	}
	if t.Failed() {
		t.Fatalf("%s baseline:\n%s", what, out)
	}
}

func helloGateBaselines(t *testing.T) (linkBase, followBase map[string]string) {
	t.Helper()
	linkOut, linkErr := helloGateRun(t, helloGateMutant{name: "baseline", pkg: "./internal/link", fixture: helloGateLinkFixture, dest: helloGateLinkDest, run: "^TestP3"})
	linkBase = helloGateLeaves(linkOut)
	helloGateRequirePass(t, "link", linkBase, helloGateLinkManifest, linkOut, linkErr)
	helloGateProof(t, linkOut)
	followOut, followErr := helloGateRun(t, helloGateMutant{name: "baseline", pkg: "./cmd/slicc", fixture: helloGateFollowFixture, dest: helloGateFollowDest, run: "^TestP3"})
	followBase = helloGateLeaves(followOut)
	helloGateRequirePass(t, "follow", followBase, helloGateFollowManifest, followOut, followErr)
	return linkBase, followBase
}

func TestHelloGateRows(t *testing.T) {
	helloGateBaselines(t)
}

func helloGateEach[T any](t *testing.T, items []T, name func(T) string, fn func(*testing.T, T)) {
	t.Helper()
	slots := make(chan struct{}, max(2, runtime.NumCPU()/2))
	var wg sync.WaitGroup
	for _, it := range items {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			t.Run(name(it), func(t *testing.T) { fn(t, it) })
		}()
	}
	wg.Wait()
}

func helloGateWantLeaves(base map[string]string, run string) []string {
	re := regexp.MustCompile(run)
	var rows []string
	for row := range base {
		if re.MatchString(row) {
			rows = append(rows, row)
		}
	}
	slices.Sort(rows)
	return rows
}

func helloGateRedSet(got map[string]string) []string {
	var red []string
	for row, result := range got {
		if result == "FAIL" {
			red = append(red, row)
		}
	}
	slices.Sort(red)
	return red
}

func helloGateCheckMutant(t *testing.T, m helloGateMutant, base map[string]string, out string, err error) {
	t.Helper()
	got := helloGateLeaves(out)
	if code := helloGateExitCode(err); code != 1 {
		t.Errorf("mutant %s: go test exited %d, want 1 for a kill", m.name, code)
	}
	for _, row := range m.red {
		if _, ok := base[row]; !ok {
			t.Errorf("mutant %s names red row %s that is not in the baseline", m.name, row)
		}
	}
	want := helloGateWantLeaves(base, m.run)
	if len(want) == 0 {
		t.Errorf("mutant %s: run %q selects no baseline row", m.name, m.run)
	}
	for _, row := range want {
		expect := "PASS"
		if slices.Contains(m.red, row) {
			expect = "FAIL"
		}
		if got[row] != expect {
			t.Errorf("mutant %s: row %s %q, want %s", m.name, row, got[row], expect)
		}
	}
	for row, result := range got {
		if !slices.Contains(want, row) {
			t.Errorf("mutant %s: row %s %s is outside the selected baseline rows", m.name, row, result)
		}
	}
	for _, row := range m.red {
		if !slices.Contains(want, row) {
			t.Errorf("mutant %s: red row %s is not selected by %q", m.name, row, m.run)
		}
	}
	t.Logf("mutant %s: exit %d, %d rows ran, red %v", m.name, helloGateExitCode(err), len(got), helloGateRedSet(got))
	helloGateProof(t, out)
	if t.Failed() {
		t.Log(out)
	}
}

func helloGateProof(t *testing.T, out string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "ci-proof "); i >= 0 {
			t.Log(line[i:])
		}
	}
}

func TestHelloGateCatchesMutants(t *testing.T) {
	helloGateRequireAnchors(t)
	helloGateSelfTest(t)
	linkBase, followBase := helloGateBaselines(t)
	helloGateEach(t, helloGateMutants(), func(m helloGateMutant) string { return m.name }, func(t *testing.T, m helloGateMutant) {
		base := linkBase
		if m.pkg == "./cmd/slicc" {
			base = followBase
		}
		out, err := helloGateRun(t, m)
		helloGateCheckMutant(t, m, base, out, err)
	})
}
