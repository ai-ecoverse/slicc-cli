package browser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DefaultLaunchTimeout = 120 * time.Second

const SlowLaunchAfter = 10 * time.Second

var ErrAlreadyRunning = errors.New("already running")

var ErrLeftRunning = errors.New("left running")

type LaunchOptions struct {
	Options
	Profile       string
	URL           string
	Headless      bool
	ExtraArgs     []string
	NoSevenFlags  bool
	Kill          bool
	LaunchTimeout time.Duration
	Slow          func(elapsed time.Duration)
}

type Browser struct {
	Installed    Installed
	Profile      string
	ProfileDir   string
	CDP          string
	WebSocketURL string
	PID          int

	cmd      *exec.Cmd
	mainPID  int
	done     chan struct{}
	waitErr  error
	stderr   *tailBuffer
	closeMu  sync.Mutex
	closeErr error
	closed   bool
	six      bool
	stale    []byte
	staleAt  os.FileInfo
	started  time.Time
}

func (b *Browser) Done() <-chan struct{} {
	return b.done
}

func (b *Browser) Err() error {
	select {
	case <-b.done:
		return b.waitErr
	default:
		return nil
	}
}

var SevenSwitches = slices.Clone(catalog.Seven.Switches)

var SevenDisabledFeatures = slices.Clone(catalog.Seven.DisabledFeatures)

var disableFeatures = catalog.Launch.DisableFeaturesFlag

func ChromiumArgs(profileDir, startURL string, headless, sevenFlags bool, extra []string) []string {
	args := make([]string, 0, len(catalog.Launch.ChromiumArgs)+len(ThrowawaySwitches)+len(SevenSwitches)+len(extra)+3)
	for _, a := range catalog.Launch.ChromiumArgs {
		args = append(args, strings.ReplaceAll(a, "{profile}", profileDir))
	}
	if Throwaway(profileDir) {
		args = append(args, ThrowawaySwitches...)
	}
	if sevenFlags {
		args = append(args, SevenSwitches...)
		args = append(args, disableFeatures+strings.Join(SevenDisabledFeatures, ","))
	}
	if headless {
		args = append(args, catalog.Launch.Headless)
	}
	args = mergeDisableFeatures(append(args, extra...))
	if startURL != "" {
		args = append(args, startURL)
	}
	return args
}

func mergeDisableFeatures(args []string) []string {
	var features []string
	seen := map[string]bool{}
	at := -1
	out := make([]string, 0, len(args))
	for _, a := range args {
		v, ok := strings.CutPrefix(a, disableFeatures)
		if !ok {
			out = append(out, a)
			continue
		}
		if at < 0 {
			at = len(out)
			out = append(out, "")
		}
		for _, f := range strings.Split(v, ",") {
			if f = strings.TrimSpace(f); f != "" && !seen[f] {
				seen[f] = true
				features = append(features, f)
			}
		}
	}
	if at < 0 {
		return out
	}
	if len(features) == 0 {
		return append(out[:at], out[at+1:]...)
	}
	out[at] = disableFeatures + strings.Join(features, ",")
	return out
}

func ElectronArgs(port int, extra []string) []string {
	return append([]string{strings.ReplaceAll(catalog.Launch.Electron.PortFlag, "{port}", strconv.Itoa(port))}, extra...)
}

func spawnCommand(in Installed, args []string, profileDir string, headless bool) (string, []string) {
	switch {
	case in.Via == "flatpak":
		pre := []string{"run"}
		if profileDir != "" {
			pre = append(pre, "--filesystem="+profileDir)
		}
		pre = append(pre, in.Package)
		return in.Executable, append(pre, args...)
	case runtime.GOOS == "darwin" && strings.HasSuffix(in.Path, ".app") && !headless:
		return "/usr/bin/open", append([]string{"-n", "-W", "-a", in.Path, "--args"}, args...)
	default:
		return in.Executable, args
	}
}

func Launch(ctx context.Context, in Installed, lo LaunchOptions) (*Browser, error) {
	if lo.LaunchTimeout <= 0 {
		lo.LaunchTimeout = DefaultLaunchTimeout
	}
	if in.Engine == EngineElectron {
		return launchElectron(ctx, in, lo)
	}
	dir, err := ProfileDir(lo.Options, in, lo.Profile)
	if err != nil {
		return nil, err
	}
	if o := lo.withDefaults(); newSixMatcher(o.platform(runtime.GOOS), true).within(dir) {
		return nil, ErrSixDir
	}
	var sites []string
	if !lo.NoSevenFlags {
		sites = SevenSites(lo.Origins)
	}
	if err := prepareProfile(dir, sites); err != nil {
		return nil, err
	}
	name, args := spawnCommand(in, ChromiumArgs(dir, lo.URL, lo.Headless, !lo.NoSevenFlags, lo.ExtraArgs), dir, lo.Headless)
	b, err := start(in, name, args)
	if err != nil {
		return nil, err
	}
	b.ProfileDir = dir
	b.Profile = filepath.Base(dir)
	ctx, cancel := context.WithTimeout(ctx, lo.LaunchTimeout)
	defer cancel()
	if err := b.waitActivePort(ctx, lo); err != nil {
		return nil, b.abortWith(err)
	}
	if pid := profileOwner(dir); pid > 0 {
		b.mainPID = pid
	}
	b.PID = b.mainPID
	return b, nil
}

func launchElectron(ctx context.Context, in Installed, lo LaunchOptions) (*Browser, error) {
	if in.DebugSupport == DebugDisabled {
		return nil, fmt.Errorf("%s blocks remote debugging", in.Name)
	}
	if findProcess(ctx, in.Executable) > 0 {
		if !lo.Kill {
			return nil, fmt.Errorf("%s is %w; quit it first, or use --kill to restart it with remote debugging", in.Name, ErrAlreadyRunning)
		}
		if err := stopRunning(ctx, in); err != nil {
			return nil, err
		}
	}
	port, err := electronPort()
	if err != nil {
		return nil, err
	}
	name, args := spawnCommand(in, ElectronArgs(port, lo.ExtraArgs), "", false)
	b, err := start(in, name, args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, lo.LaunchTimeout)
	defer cancel()
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	if err := b.waitVersion(ctx, lo, candidate{base: base, source: SourceFlag}); err != nil {
		return nil, b.abortWith(err)
	}
	if b.mainPID == 0 {
		b.mainPID = findProcess(ctx, in.Executable)
	}
	b.PID = b.mainPID
	return b, nil
}

func start(in Installed, name string, args []string) (*Browser, error) {
	cmd := exec.Command(name, args...)
	tail := &tailBuffer{max: 4096}
	cmd.Stdout, cmd.Stderr = tail, tail
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", in.Name, err)
	}
	b := &Browser{Installed: in, cmd: cmd, done: make(chan struct{}), stderr: tail, started: time.Now()}
	if !strings.HasSuffix(name, "/open") {
		b.mainPID = cmd.Process.Pid
	}
	go func() {
		b.waitErr = cmd.Wait()
		close(b.done)
	}()
	return b, nil
}

func (b *Browser) exitError(what string) error {
	how := "exited"
	if s := b.cmd.ProcessState; s != nil && s.Exited() {
		how = fmt.Sprintf("exited with code %d", s.ExitCode())
	} else if b.waitErr != nil {
		how = "exited (" + b.waitErr.Error() + ")"
	}
	msg := strings.TrimSpace(b.stderr.String())
	if msg == "" {
		msg = "no output"
	}
	return fmt.Errorf("%s %s after %s, before %s; its output: %s", b.Installed.Name, how, b.elapsed(), what, msg)
}

func (b *Browser) elapsed() time.Duration {
	return time.Since(b.started).Round(100 * time.Millisecond)
}

type slowNotice struct {
	report func(time.Duration)
	next   time.Time
	start  time.Time
}

func newSlowNotice(start time.Time, report func(time.Duration)) *slowNotice {
	return &slowNotice{report: report, start: start, next: start.Add(SlowLaunchAfter)}
}

func (n *slowNotice) check(now time.Time) {
	if n.report == nil || now.Before(n.next) {
		return
	}
	n.report(n.next.Sub(n.start))
	for !now.Before(n.next) {
		n.next = n.next.Add(SlowLaunchAfter)
	}
}

func (b *Browser) stillRunning(ctx context.Context, what string) error {
	return fmt.Errorf("%s is still running after %s but %s: %w", b.Installed.Name, b.elapsed(), what, ctx.Err())
}

func (b *Browser) waitActivePort(ctx context.Context, lo LaunchOptions) error {
	o := lo.withDefaults()
	client := o.httpClient()
	slow := newSlowNotice(b.started, lo.Slow)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if port, ws, err := b.freshActivePort(); err == nil {
			c := candidate{base: "http://127.0.0.1:" + strconv.Itoa(port), wsPath: ws, source: SourceActivePort, dir: b.ProfileDir, six: b.six}
			if ep, err := probeCandidate(ctx, client, c, o); err == nil && !ep.WSOnly {
				b.CDP, b.WebSocketURL = ep.CDP, ep.WebSocketURL
				return nil
			}
		}
		select {
		case <-b.done:
			return b.exitError("writing " + ActivePortFile)
		case <-ctx.Done():
			if b.six {
				return b.stillRunning(ctx, "has not written "+ActivePortFile+" in SLICC six's profile")
			}
			return b.stillRunning(ctx, "has not written "+filepath.Join(b.ProfileDir, ActivePortFile))
		case now := <-tick.C:
			slow.check(now)
		}
	}
}

func sameFile(a, b os.FileInfo) bool {
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func (b *Browser) freshActivePort() (int, string, error) {
	name := filepath.Join(b.ProfileDir, ActivePortFile)
	if b.staleAt != nil {
		info, err := os.Stat(name)
		if err != nil {
			return 0, "", err
		}
		if sameFile(info, b.staleAt) {
			return 0, "", errors.New("stale")
		}
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return 0, "", err
	}
	if b.stale != nil && bytes.Equal(data, b.stale) {
		return 0, "", errors.New("stale")
	}
	port, ws, ok := ParseActivePort(string(data))
	if !ok {
		return 0, "", errors.New("malformed")
	}
	return port, ws, nil
}

func (b *Browser) waitVersion(ctx context.Context, lo LaunchOptions, c candidate) error {
	o := lo.withDefaults()
	client := o.httpClient()
	slow := newSlowNotice(b.started, lo.Slow)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if ep, err := probeCandidate(ctx, client, c, o); err == nil {
			b.CDP, b.WebSocketURL = ep.CDP, ep.WebSocketURL
			return nil
		}
		select {
		case <-b.done:
			return b.exitError("its CDP port answered")
		case <-ctx.Done():
			return b.stillRunning(ctx, "has no CDP on "+c.base)
		case now := <-tick.C:
			slow.check(now)
		}
	}
}

func (b *Browser) abort() {
	signalTree(b.groupPID(), b.mainPID, true)
	select {
	case <-b.done:
	case <-time.After(5 * time.Second):
	}
}

func (b *Browser) abortWith(err error) error {
	b.abort()
	if b.mainPID != 0 {
		return err
	}
	if left := b.killStrays(appSettle, appGiveUp); len(left) > 0 {
		return fmt.Errorf("%w; %w: %s is still running as process %v", err, ErrLeftRunning, b.Installed.Name, left)
	}
	return err
}

const (
	appSettle = 3 * time.Second
	appGiveUp = 15 * time.Second
)

func (b *Browser) killStrays(settle, giveUp time.Duration) []int {
	start := time.Now()
	quiet := start.Add(settle)
	for {
		pids := b.strays()
		for _, pid := range pids {
			signalTree(0, pid, true)
		}
		if len(pids) > 0 {
			quiet = time.Now().Add(settle)
		}
		if now := time.Now(); now.After(quiet) || now.Sub(start) > giveUp {
			return b.strays()
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (b *Browser) strays() []int {
	if b.Installed.Engine != EngineElectron {
		return profilePIDs(b.ProfileDir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if pid := findProcess(ctx, b.Installed.Executable); pid > 1 && pid != os.Getpid() {
		return []int{pid}
	}
	return nil
}

func profilePIDs(dir string) []int {
	if dir == "" {
		return nil
	}
	want := resolvedPath(dir)
	var pids []int
	if pid := profileOwner(dir); pid > 1 && pid != os.Getpid() {
		pids = append(pids, pid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := processTable(ctx)
	if err != nil {
		return pids
	}
	for _, h := range parseProcessTable(out, runtime.GOOS == "windows", os.Getpid()) {
		if h.pid > 1 && h.dir != "" && samePath(resolvedPath(h.dir), want) && pidAlive(h.pid) && !slices.Contains(pids, h.pid) {
			pids = append(pids, h.pid)
		}
	}
	return pids
}

func (b *Browser) groupPID() int {
	if b.cmd == nil || b.cmd.Process == nil {
		return 0
	}
	return b.cmd.Process.Pid
}

func (b *Browser) Close(ctx context.Context) error {
	b.closeMu.Lock()
	defer b.closeMu.Unlock()
	if b.closed {
		return b.closeErr
	}
	b.closed = true
	b.closeErr = b.shutdown(ctx)
	return b.closeErr
}

func (b *Browser) shutdown(ctx context.Context) error {
	if b.exited() {
		return nil
	}
	if b.WebSocketURL != "" {
		if cdpBrowserCall(ctx, b.WebSocketURL, "Browser.close", 2*time.Second) == nil && b.waitExit(ctx, 5*time.Second) {
			return nil
		}
	}
	signalTree(b.groupPID(), b.mainPID, false)
	if b.waitExit(ctx, 3*time.Second) {
		return nil
	}
	signalTree(b.groupPID(), b.mainPID, true)
	if b.waitExit(ctx, 3*time.Second) {
		return nil
	}
	return fmt.Errorf("%s (pid %d) did not exit", b.Installed.Name, b.mainPID)
}

func (b *Browser) exited() bool {
	select {
	case <-b.done:
		return b.mainPID == 0 || !pidAlive(b.mainPID)
	default:
		return false
	}
}

func (b *Browser) waitExit(ctx context.Context, d time.Duration) bool {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if b.exited() {
			return true
		}
		select {
		case <-ctx.Done():
			return b.exited()
		case <-deadline.C:
			return b.exited()
		case <-tick.C:
		}
	}
}

func electronPort() (int, error) {
	for port := catalog.Launch.Electron.MinPort; port <= 65535; port++ {
		if slices.Contains(catalog.Launch.Electron.ReservedPorts, port) {
			continue
		}
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			continue
		}
		_ = l.Close()
		return port, nil
	}
	return 0, errors.New("no free local port for remote debugging")
}

type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
