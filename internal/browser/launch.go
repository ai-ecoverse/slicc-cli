package browser

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DefaultLaunchTimeout = 30 * time.Second

var ErrAlreadyRunning = errors.New("already running")

type LaunchOptions struct {
	Options
	Profile       string
	URL           string
	Headless      bool
	ExtraArgs     []string
	NoSevenFlags  bool
	LaunchTimeout time.Duration
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

var SevenSwitches = []string{
	"--disable-background-timer-throttling",
	"--disable-backgrounding-occluded-windows",
	"--disable-renderer-backgrounding",
	"--disable-background-tracing",
}

var SevenDisabledFeatures = []string{
	"LocalNetworkAccessChecks",
	"LocalNetworkAccessChecksWebSockets",
	"IntensiveWakeUpThrottling",
	"HighEfficiencyModeAvailable",
	"InfiniteTabsFreezing",
	"InfiniteTabsFreezingOnMemoryPressure",
	"CPUMeasurementInFreezingPolicy",
	"MemoryMeasurementInFreezingPolicy",
	"AllowDevtoolsConnectedDiscard",
}

const disableFeatures = "--disable-features="

func ChromiumArgs(profileDir, startURL string, headless, sevenFlags bool, extra []string) []string {
	args := []string{
		"--remote-debugging-port=0",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}
	if sevenFlags {
		args = append(args, SevenSwitches...)
		args = append(args, disableFeatures+strings.Join(SevenDisabledFeatures, ","))
	}
	if headless {
		args = append(args, "--headless=new")
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
	return append([]string{"--remote-debugging-port=" + strconv.Itoa(port)}, extra...)
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

func launchElectron(ctx context.Context, in Installed, lo LaunchOptions) (*Browser, error) {
	if in.DebugSupport == DebugDisabled {
		return nil, fmt.Errorf("%s blocks remote debugging", in.Name)
	}
	if findProcess(ctx, in.Executable) > 0 {
		return nil, fmt.Errorf("%s is %w; quit it first so it can start with remote debugging", in.Name, ErrAlreadyRunning)
	}
	port, err := freePort()
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
	if err := b.waitVersion(ctx, lo.Options, candidate{base: base, source: SourceFlag}); err != nil {
		b.abort()
		return nil, err
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
	b := &Browser{Installed: in, cmd: cmd, done: make(chan struct{}), stderr: tail}
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
	msg := strings.TrimSpace(b.stderr.String())
	if msg != "" {
		msg = ": " + msg
	}
	return fmt.Errorf("%s exited before %s (%v)%s", b.Installed.Name, what, b.waitErr, msg)
}

func (b *Browser) waitActivePort(ctx context.Context, o Options) error {
	o = o.withDefaults()
	client := &http.Client{Timeout: o.Timeout}
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if port, ws, err := ReadActivePort(b.ProfileDir); err == nil {
			c := candidate{base: "http://127.0.0.1:" + strconv.Itoa(port), wsPath: ws, source: SourceActivePort, dir: b.ProfileDir}
			if ep, err := probeCandidate(ctx, client, c, o); err == nil && !ep.WSOnly {
				b.CDP, b.WebSocketURL = ep.CDP, ep.WebSocketURL
				return nil
			}
		}
		select {
		case <-b.done:
			return b.exitError("writing " + ActivePortFile)
		case <-ctx.Done():
			return fmt.Errorf("%s did not write %s in %s: %w", b.Installed.Name, filepath.Join(b.ProfileDir, ActivePortFile), b.ProfileDir, ctx.Err())
		case <-tick.C:
		}
	}
}

func (b *Browser) waitVersion(ctx context.Context, o Options, c candidate) error {
	o = o.withDefaults()
	client := &http.Client{Timeout: o.Timeout}
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
			return fmt.Errorf("%s: no CDP on %s: %w", b.Installed.Name, c.base, ctx.Err())
		case <-tick.C:
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

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
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
