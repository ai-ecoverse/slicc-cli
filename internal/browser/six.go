package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const SixProfile = "six"

var (
	TierSix   = catalog.Rank.Tiers.Six
	sixInfo   = catalog.Profiles.Six
	sixNameRE = regexp.MustCompile("(?i)" + catalog.Profiles.SixProfilePattern)
)

var ErrSixBusy = errors.New("six's Chrome is using this profile; close it, or use --browser/--profile to pick another")

var ErrSixDir = errors.New("that profile is SLICC six's Chrome profile, which slicc never prepares; use --profile six to use it in place")

var ErrSixMissing = errors.New("SLICC six's Chrome profile doesn't exist; start SLICC six once, or pick another --profile")

type Six struct {
	Dir   string
	Label string
	Port  int
}

func ParseSixProfile(s string) (int, bool, error) {
	if s == SixProfile {
		return 0, true, nil
	}
	rest, ok := strings.CutPrefix(s, SixProfile+":")
	if !ok {
		return 0, false, nil
	}
	port, err := strconv.Atoi(rest)
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != rest {
		return 0, true, fmt.Errorf("--profile %s: the port after six: must be 1-65535", s)
	}
	return port, true, nil
}

func SixLabel(port int) string {
	if port == 0 || port == sixInfo.DefaultPort {
		return SixProfile
	}
	return SixProfile + ":" + strconv.Itoa(port)
}

func (p platform) sixRoot() string {
	r := sixInfo.Roots.Linux
	switch p.goos {
	case "darwin":
		r = sixInfo.Roots.MacOS
	case "windows":
		r = sixInfo.Roots.Windows
	}
	base := p.getenv(r.Base.Env)
	if r.Base.Env == "" || base == "" {
		base = p.join(append([]string{p.home}, r.Base.Home...)...)
	}
	return p.join(append([]string{base}, r.Path...)...)
}

func (p platform) sixEnvDir() string {
	return strings.TrimSpace(p.getenv(sixInfo.UserDataDirEnv))
}

func sixName(port int) string {
	if port == 0 || port == sixInfo.DefaultPort {
		return sixInfo.DefaultName
	}
	return sixInfo.DefaultName + strings.ReplaceAll(sixInfo.PortSuffix, "{port}", strconv.Itoa(port))
}

func (p platform) sixDir(port int) string {
	if d := p.sixEnvDir(); d != "" {
		return d
	}
	return p.join(p.sixRoot(), sixName(port))
}

func SixRootFor(goos string, o Options) string {
	o = o.withDefaults()
	return o.platform(goos).sixRoot()
}

func SixProfileDirFor(goos string, o Options, port int) string {
	o = o.withDefaults()
	return o.platform(goos).sixDir(port)
}

func SixProfileFor(o Options, port int) Six {
	return Six{Dir: SixProfileDirFor(runtime.GOOS, o, port), Label: SixLabel(port), Port: port}
}

func sixPort(name string) (int, bool) {
	if !sixNameRE.MatchString(name) {
		return 0, false
	}
	rest := name[len(sixInfo.DefaultName):]
	if rest == "" {
		return 0, true
	}
	n, err := strconv.Atoi(strings.TrimLeft(rest, "-"))
	if err != nil {
		return 0, false
	}
	return n, true
}

func FindSixProfiles(o Options) []Six {
	if !discoveryFor(o).profiles {
		return nil
	}
	o = o.withDefaults()
	p := o.platform(runtime.GOOS)
	if d := p.sixEnvDir(); d != "" {
		if isDir(d) {
			return []Six{{Dir: d, Label: SixProfile}}
		}
		return nil
	}
	var out []Six
	for _, d := range p.sixRootDirs() {
		port, _ := sixPort(filepath.Base(d))
		out = append(out, Six{Dir: d, Label: SixLabel(port), Port: port})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

func (p platform) sixRootDirs() []string {
	root := p.sixRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !sixNameRE.MatchString(e.Name()) {
			continue
		}
		if d := filepath.Join(root, e.Name()); isDir(d) {
			out = append(out, d)
		}
	}
	return out
}

func (p platform) sixDirs() []string {
	dirs := p.sixRootDirs()
	if d := p.sixEnvDir(); d != "" && isDir(d) {
		dirs = append(dirs, d)
	}
	return dirs
}

type sixMatcher struct {
	on    bool
	roots []string
	envs  []string
}

func newSixMatcher(p platform, on bool) sixMatcher {
	if !on {
		return sixMatcher{}
	}
	m := sixMatcher{on: true, roots: withReal(p.sixRoot())}
	if d := p.sixEnvDir(); d != "" {
		m.envs = withReal(d)
	}
	return m
}

func withReal(dir string) []string {
	out := []string{dir}
	if r, err := filepath.EvalSymlinks(dir); err == nil && r != dir {
		out = append(out, r)
	}
	return out
}

func samePath(a, b string) bool {
	return strings.EqualFold(normPath(a), normPath(b))
}

func normPath(s string) string {
	s = path.Clean(strings.ReplaceAll(s, `\`, "/"))
	if len(s) > 1 {
		s = strings.TrimRight(s, "/")
	}
	return s
}

func (m sixMatcher) is(dir string) bool {
	if !m.on || dir == "" {
		return false
	}
	for _, d := range withReal(dir) {
		for _, e := range m.envs {
			if samePath(d, e) {
				return true
			}
		}
		n := normPath(d)
		i := strings.LastIndexByte(n, '/')
		if i < 0 || !sixNameRE.MatchString(n[i+1:]) {
			continue
		}
		for _, r := range m.roots {
			if samePath(n[:i], r) {
				return true
			}
		}
	}
	return false
}

func SameDir(a, b string) bool {
	for _, x := range withReal(a) {
		for _, y := range withReal(b) {
			if samePath(x, y) {
				return true
			}
		}
	}
	return false
}

func SixName(six Six) string {
	name := "Chrome · SLICC six profile"
	if six.Port != 0 && six.Port != sixInfo.DefaultPort {
		name += " " + strconv.Itoa(six.Port)
	}
	return name
}

func SixProfileEntry(six Six, in *Installed) Entry {
	e := Entry{ID: six.Label, Kind: KindSix, Tier: TierSix, Name: SixName(six), Installed: in, Six: &six}
	if in != nil {
		e.Version = in.Version
	}
	return e
}

func IsSixDir(o Options, dir string) bool {
	o = o.withDefaults()
	return newSixMatcher(o.platform(runtime.GOOS), true).is(dir)
}

func SixEntry(ep Endpoint) Entry {
	ep.Six = true
	return Entry{ID: EndpointID(ep), Kind: KindEndpoint, Tier: TierSix, Name: ep.Name, Version: ep.Version, Endpoint: &ep}
}

func ProbeSix(ctx context.Context, o Options, dir string) (*Endpoint, error) {
	if !isDir(dir) {
		return nil, ErrSixMissing
	}
	if !lockHeld(dir) {
		return nil, nil
	}
	port, ws, err := ReadActivePort(dir)
	if err != nil {
		return nil, ErrSixBusy
	}
	o = o.withDefaults()
	c := candidate{base: "http://127.0.0.1:" + strconv.Itoa(port), wsPath: ws, source: SourceActivePort, dir: dir, six: true}
	ep, err := probeCandidate(ctx, o.httpClient(), c, o)
	if err != nil || ep.WSOnly {
		return nil, ErrSixBusy
	}
	ep.CDP = normalizedCDP(ep.CDP)
	return ep, nil
}

var sixVersionRE = regexp.MustCompile(`^\d+(\.\d+){3}`)

func sixLastVersion(dir string) string {
	if b, err := readSmall(filepath.Join(dir, "Last Version"), 1<<10); err == nil {
		if v := sixVersionRE.FindString(strings.TrimSpace(string(b))); v != "" {
			return v
		}
	}
	b, err := readSmall(filepath.Join(dir, "Local State"), 32<<20)
	if err != nil {
		return ""
	}
	var ls struct {
		Metrics struct {
			Stability struct {
				StatsVersion string `json:"stats_version"`
			} `json:"stability"`
		} `json:"user_experience_metrics"`
	}
	if json.NewDecoder(bytes.NewReader(b)).Decode(&ls) != nil {
		return ""
	}
	return sixVersionRE.FindString(ls.Metrics.Stability.StatsVersion)
}

func readSmall(name string, limit int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, limit))
}

func SixBrowser(dir string, installed []Installed) (Installed, bool) {
	var ins []Installed
	for _, in := range installed {
		if k, ok := knownByID(in.ID); ok && k.chromium && in.Engine == EngineChromium {
			ins = append(ins, in)
		}
	}
	sortInstalled(ins)
	if v := sixLastVersion(dir); v != "" {
		for _, in := range ins {
			if in.Version == v {
				return in, true
			}
		}
	}
	for _, in := range ins {
		if in.ID == catalog.Rank.ChromeID {
			return in, true
		}
	}
	return Installed{}, false
}

func LaunchSix(ctx context.Context, in Installed, six Six, lo LaunchOptions) (*Browser, error) {
	if lo.LaunchTimeout <= 0 {
		lo.LaunchTimeout = DefaultLaunchTimeout
	}
	if in.Engine != EngineChromium {
		return nil, fmt.Errorf("%s can't open SLICC six's Chrome profile", in.Name)
	}
	dir := six.Dir
	if !isDir(dir) {
		return nil, ErrSixMissing
	}
	if lockHeld(dir) {
		return nil, ErrSixBusy
	}
	stale, _ := os.ReadFile(filepath.Join(dir, ActivePortFile))
	name, args := spawnCommand(in, ChromiumArgs(dir, lo.URL, lo.Headless, !lo.NoSevenFlags, lo.ExtraArgs), dir, lo.Headless)
	b, err := start(in, name, args)
	if err != nil {
		return nil, err
	}
	b.ProfileDir = dir
	b.Profile = six.Label
	b.six = true
	b.stale = stale
	ctx, cancel := context.WithTimeout(ctx, lo.LaunchTimeout)
	defer cancel()
	if err := b.waitActivePort(ctx, lo.Options); err != nil {
		b.abort()
		return nil, err
	}
	if pid := profileOwner(dir); pid > 0 {
		b.mainPID = pid
	}
	b.PID = b.mainPID
	return b, nil
}

func SixExists(dir string) error {
	if !isDir(dir) {
		return ErrSixMissing
	}
	return nil
}

func SixHeld(dir string) bool {
	return lockHeld(dir)
}
