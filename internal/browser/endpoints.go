package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

var ActivePortFile = catalog.Discovery.ActivePortFile

type candidate struct {
	base     string
	wsPath   string
	source   Source
	browser  string
	dir      string
	profile  string
	app      string
	headless bool
	six      bool
}

type versionInfo struct {
	Browser      string `json:"Browser"`
	UserAgent    string `json:"User-Agent"`
	WebSocketURL string `json:"webSocketDebuggerUrl"`
}

type listTarget struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

func ParseActivePort(contents string) (int, string, bool) {
	lines := strings.Split(strings.ReplaceAll(contents, "\r\n", "\n"), "\n")
	port, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || port <= 0 || port > 65535 {
		return 0, "", false
	}
	if len(lines) < 2 {
		return 0, "", false
	}
	ws := strings.TrimSpace(lines[1])
	if !strings.HasPrefix(ws, "/") {
		return 0, "", false
	}
	return port, ws, true
}

func ReadActivePort(dir string) (int, string, error) {
	b, err := os.ReadFile(filepath.Join(dir, ActivePortFile))
	if err != nil {
		return 0, "", err
	}
	port, ws, ok := ParseActivePort(string(b))
	if !ok {
		return 0, "", fmt.Errorf("%s: malformed", filepath.Join(dir, ActivePortFile))
	}
	return port, ws, nil
}

func NormalizeCDPURL(raw string) (string, string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", errors.New("empty CDP URL")
	}
	if _, err := strconv.Atoi(s); err == nil {
		s = "127.0.0.1:" + s
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("invalid CDP URL %q", raw)
	}
	scheme := strings.ToLower(u.Scheme)
	ws := ""
	switch scheme {
	case "http", "https":
	case "ws":
		scheme, ws = "http", u.Path
	case "wss":
		scheme, ws = "https", u.Path
	default:
		return "", "", fmt.Errorf("invalid CDP URL %q: scheme %s", raw, u.Scheme)
	}
	if u.Port() == "" {
		return "", "", fmt.Errorf("invalid CDP URL %q: no port", raw)
	}
	if ws == "/" {
		ws = ""
	}
	return scheme + "://" + u.Host, ws, nil
}

func (o Options) withDefaults() Options {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	if o.ProbePorts == nil {
		o.ProbePorts = DefaultProbePorts
	}
	if o.Timeout <= 0 {
		o.Timeout = time.Second
	}
	return o
}

func (o Options) httpClient() *http.Client {
	return cdp.EndpointClient(o.Transport, o.Timeout)
}

func (o Options) platform(goos string) platform {
	return platform{goos: goos, home: o.Home, env: o.Getenv}
}

func (o Options) profilesDir(p platform) string {
	if o.ProfilesDir != "" {
		return o.ProfilesDir
	}
	return p.profilesDir()
}

func activePortCandidates(o Options, p platform, d discovery) []candidate {
	var out []candidate
	if d.userData {
		for _, k := range knownBrowsers {
			for _, dir := range p.userDataDirs(k) {
				out = appendActivePort(out, dir, k.id, "")
			}
		}
	}
	if !d.profiles {
		return out
	}
	for _, dir := range p.sixDirs() {
		out = appendActivePort(out, dir, "", "")
	}
	roots := []string{o.profilesDir(p)}
	if p.goos == "linux" {
		for _, k := range knownBrowsers {
			if k.linux.snap != "" {
				roots = append(roots, p.snapProfilesDir(k.linux.snap))
			}
		}
	}
	for _, root := range uniqueStrings(roots) {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				out = appendActivePort(out, filepath.Join(root, e.Name()), "", e.Name())
			}
		}
	}
	return out
}

func appendActivePort(out []candidate, dir, browserID, profile string) []candidate {
	port, ws, err := ReadActivePort(dir)
	if err != nil {
		return out
	}
	return append(out, candidate{
		base: "http://127.0.0.1:" + strconv.Itoa(port), wsPath: ws, source: SourceActivePort,
		browser: browserID, dir: dir, profile: profile,
	})
}

func DiscoverEndpoints(ctx context.Context, o Options) ([]Endpoint, []error) {
	d := discoveryFor(o)
	o = o.withDefaults()
	p := o.platform(runtime.GOOS)
	var cands []candidate
	cands = append(cands, activePortCandidates(o, p, d)...)
	var errs []error
	for _, raw := range o.CDPURLs {
		base, ws, err := NormalizeCDPURL(raw)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		cands = append(cands, candidate{base: base, wsPath: ws, source: SourceFlag})
	}
	six := newSixMatcher(p, d.profiles)
	cands = append(cands, processCandidates(ctx, o, d, six)...)
	if !o.NoProbe && d.probe {
		for _, port := range o.ProbePorts {
			cands = append(cands, candidate{base: "http://127.0.0.1:" + strconv.Itoa(port), source: SourceProbe})
		}
	}
	markSix(cands, six)
	results := make([]*Endpoint, len(cands))
	resErrs := make([]error, len(cands))
	var wg sync.WaitGroup
	client := o.httpClient()
	for i, c := range cands {
		wg.Add(1)
		go func(i int, c candidate) {
			defer wg.Done()
			results[i], resErrs[i] = probeCandidate(ctx, client, c, o)
		}(i, c)
	}
	wg.Wait()
	for i, c := range cands {
		if resErrs[i] != nil && c.source == SourceFlag {
			errs = append(errs, fmt.Errorf("%s: %w", c.base, resErrs[i]))
		}
	}
	return mergeEndpoints(cands, results), errs
}

func ProbeEndpoint(ctx context.Context, raw string, o Options) (Endpoint, error) {
	o = o.withDefaults()
	base, ws, err := NormalizeCDPURL(raw)
	if err != nil {
		return Endpoint{}, err
	}
	ep, err := probeCandidate(ctx, o.httpClient(), candidate{base: base, wsPath: ws, source: SourceFlag}, o)
	if err != nil {
		return Endpoint{}, err
	}
	return *ep, nil
}

func probeCandidate(ctx context.Context, client *http.Client, c candidate, o Options) (*Endpoint, error) {
	var vi versionInfo
	err := getJSON(ctx, client, c.base+"/json/version", &vi)
	if err == nil && vi.WebSocketURL == "" {
		err = errors.New("no webSocketDebuggerUrl in /json/version")
	}
	if err != nil {
		if c.wsPath != "" && tcpOpen(ctx, c.base, o) {
			return wsOnlyEndpoint(c), nil
		}
		return nil, err
	}
	if c.wsPath != "" {
		if u, perr := url.Parse(vi.WebSocketURL); perr != nil || u.Path != c.wsPath {
			return nil, fmt.Errorf("stale %s: %s answers for another browser", ActivePortFile, c.base)
		}
	}
	ep := endpointFromVersion(c, vi)
	var targets []listTarget
	if err := getJSON(ctx, client, c.base+"/json/list", &targets); err == nil {
		for _, t := range targets {
			if t.Type == "page" && IsSevenOrigin(OriginOf(t.URL), o.Origins) {
				ep.SevenTabs = append(ep.SevenTabs, t.tab())
			}
		}
	}
	ep.SevenOpen = len(ep.SevenTabs) > 0
	return &ep, nil
}

func (t listTarget) tab() Tab {
	return Tab{ID: t.ID, URL: t.URL, Title: t.Title}
}

func wsOnlyEndpoint(c candidate) *Endpoint {
	ep := Endpoint{
		CDP: c.base, Source: c.source, Browser: c.browser, UserDataDir: c.dir, Profile: c.profile,
		WSOnly: true, Six: c.six, SevenTabs: []Tab{},
	}
	ep.WebSocketURL = "ws" + strings.TrimPrefix(c.base, "http") + c.wsPath
	ep.Name = catalog.Discovery.Names.Fallback
	if k, ok := knownByID(c.browser); ok {
		ep.Name = k.name
	}
	return &ep
}

var productRE = regexp.MustCompile(`^([^/]+)/(\S+)`)

var chromeMajorRE = regexp.MustCompile(catalog.Discovery.ChromeMajorPattern)

func endpointFromVersion(c candidate, vi versionInfo) Endpoint {
	ep := Endpoint{
		CDP: c.base, WebSocketURL: vi.WebSocketURL, Product: vi.Browser, UserAgent: vi.UserAgent,
		Source: c.source, Browser: c.browser, UserDataDir: c.dir, Profile: c.profile, Six: c.six, SevenTabs: []Tab{},
	}
	product := vi.Browser
	if m := productRE.FindStringSubmatch(vi.Browser); m != nil {
		product, ep.Version = m[1], m[2]
	}
	names := catalog.Discovery.Names
	switch {
	case c.browser != "":
		k, _ := knownByID(c.browser)
		ep.Name = k.name
	case strings.Contains(vi.UserAgent, names.ElectronUserAgent):
		ep.Name = names.Electron
	case names.Products[product] != "":
		ep.Name = names.Products[product]
	case product == "":
		ep.Name = names.Fallback
	default:
		ep.Name = product
	}
	return ep
}

func (e Endpoint) ChromiumMajor() int {
	for _, s := range []string{e.UserAgent, e.Product} {
		if m := chromeMajorRE.FindStringSubmatch(s); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return 0
}

func (e Endpoint) Headless() bool {
	return e.headless || strings.Contains(e.UserAgent, "HeadlessChrome") || strings.HasPrefix(e.Product, "HeadlessChrome/")
}

func getJSON(ctx context.Context, client *http.Client, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", u, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

func tcpOpen(ctx context.Context, base string, o Options) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	dial := o.Dial
	if dial == nil {
		d := net.Dialer{Timeout: o.Timeout}
		dial = d.DialContext
	}
	conn, err := dial(ctx, "tcp", u.Host)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func endpointKey(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "::1" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, u.Port())
}

func EndpointKey(raw string) string {
	base, _, err := NormalizeCDPURL(raw)
	if err != nil {
		return ""
	}
	return endpointKey(base)
}

func normalizedCDP(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" {
		return base
	}
	return strings.ToLower(u.Scheme) + "://" + endpointKey(base)
}

func sourceOrder(s Source) int {
	if n, ok := catalog.Discovery.SourceRank[string(s)]; ok {
		return n
	}
	return len(catalog.Discovery.SourceRank)
}

func markSix(cands []candidate, six sixMatcher) {
	for i := range cands {
		cands[i].six = cands[i].six || six.is(cands[i].dir)
	}
}

func mergeEndpoints(cands []candidate, results []*Endpoint) []Endpoint {
	best := map[string]*Endpoint{}
	for i, ep := range results {
		if ep == nil {
			continue
		}
		if major := ep.ChromiumMajor(); major > 0 && major < MinChromiumMajor {
			continue
		}
		key := endpointKey(cands[i].base)
		if cur, ok := best[key]; ok && sourceOrder(cur.Source) <= sourceOrder(ep.Source) {
			continue
		}
		best[key] = ep
	}
	apps := map[string]string{}
	headless := map[string]bool{}
	for i, ep := range results {
		if ep == nil {
			continue
		}
		key := endpointKey(cands[i].base)
		if cands[i].app != "" && apps[key] == "" {
			apps[key] = cands[i].app
		}
		headless[key] = headless[key] || cands[i].headless
	}
	sixDirs := map[string]string{}
	for i, ep := range results {
		if ep != nil && ep.Six {
			if key := endpointKey(cands[i].base); sixDirs[key] == "" {
				sixDirs[key] = ep.UserDataDir
			}
		}
	}
	out := make([]Endpoint, 0, len(best))
	for key, ep := range best {
		e := *ep
		e.App, e.headless = apps[key], headless[key]
		if dir, ok := sixDirs[key]; ok {
			e.Six = true
			if e.UserDataDir == "" {
				e.UserDataDir = dir
			}
		}
		e.CDP = normalizedCDP(e.CDP)
		out = append(out, e)
	}
	sortEndpoints(out)
	return out
}

func sortEndpoints(eps []Endpoint) {
	sort.SliceStable(eps, func(i, j int) bool {
		hi, pi := splitHostPort(eps[i].CDP)
		hj, pj := splitHostPort(eps[j].CDP)
		if hi != hj {
			return hi < hj
		}
		if pi != pj {
			return pi < pj
		}
		return eps[i].CDP < eps[j].CDP
	})
}

func splitHostPort(base string) (string, int) {
	host, port, _ := net.SplitHostPort(endpointKey(base))
	n, _ := strconv.Atoi(port)
	return host, n
}
