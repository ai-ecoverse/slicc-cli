package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

const ProtectedPortsEnv = "SLICC_PROTECTED_PORTS"

var (
	watchEvery    = 5 * time.Second
	processEvery  = time.Second
	processIdle   = 30 * time.Second
	processActive = 30 * time.Second
	processWait   = 5 * time.Second
)

var errNoProcessList = errors.New("couldn't list running browsers")

const (
	firstDebugPort = 9222
	lastDebugPort  = 9229
)

func DebugPortRange() ([]uint16, error) {
	ports := portRange(firstDebugPort, lastDebugPort)
	v := strings.TrimSpace(os.Getenv(ProtectedPortsEnv))
	if v == "" {
		return ports, nil
	}
	a, b, ok := strings.Cut(v, "-")
	if !ok {
		b = a
	}
	first, err1 := strconv.ParseUint(strings.TrimSpace(a), 10, 16)
	last, err2 := strconv.ParseUint(strings.TrimSpace(b), 10, 16)
	if err1 != nil || err2 != nil || first == 0 || last < first {
		return nil, fmt.Errorf("%s=%q: want <first>-<last>", ProtectedPortsEnv, v)
	}
	return union(ports, portRange(uint16(first), uint16(last))), nil
}

func portRange(first, last uint16) []uint16 {
	var out []uint16
	for p := int(first); p <= int(last); p++ {
		out = append(out, uint16(p))
	}
	return out
}

func union(lists ...[]uint16) []uint16 {
	var out []uint16
	for _, l := range lists {
		for _, p := range l {
			if p != 0 && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

func ProcessPorts(ctx context.Context, o Options) ([]uint16, error) {
	table := o.Processes
	if table == nil {
		table = processTable
	}
	out, err := table(ctx)
	if err != nil {
		return nil, err
	}
	var ports []uint16
	for _, h := range parseProcessTable(out, runtime.GOOS == "windows", os.Getpid()) {
		port := h.port
		if port == 0 && h.dir != "" {
			port, _, _ = ReadActivePort(h.dir)
		}
		if port > 0 && port <= 65535 {
			ports = union(ports, []uint16{uint16(port)})
		}
	}
	return ports, nil
}

type scanRun struct {
	start time.Time
	done  chan struct{}
	err   error
}

type liveScan struct {
	ctx     context.Context
	o       Options
	logf    func(string, ...any)
	every   time.Duration
	idle    time.Duration
	active  time.Duration
	wait    time.Duration
	wake    chan struct{}
	mu      sync.Mutex
	proc    []uint16
	okAt    time.Time
	run     *scanRun
	took    time.Duration
	touched time.Time
	busy    bool
	failed  bool
}

func newLiveScan(ctx context.Context, o Options, logf func(string, ...any)) *liveScan {
	return &liveScan{ctx: ctx, o: o, logf: logf, every: processEvery, idle: processIdle, active: processActive, wait: processWait, wake: make(chan struct{}, 1)}
}

func (s *liveScan) snapshot() []uint16 {
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	return union(ActivePorts(s.o), proc)
}

func (s *liveScan) startLocked() *scanRun {
	if s.run != nil {
		return s.run
	}
	r := &scanRun{start: time.Now(), done: make(chan struct{})}
	s.run = r
	go s.scan(r)
	return r
}

func (s *liveScan) scan(r *scanRun) {
	ctx, cancel := context.WithTimeout(s.ctx, s.wait)
	ports, err := ProcessPorts(ctx, s.o)
	cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer close(r.done)
	s.run, s.took = nil, time.Since(r.start)
	if err != nil {
		r.err = err
		if s.ctx.Err() != nil || s.failed {
			return
		}
		s.failed = true
		if s.logf != nil {
			s.logf("couldn't list running browsers for their debugging ports: %v", err)
		}
		return
	}
	s.proc, s.okAt = ports, r.start
	if s.failed {
		s.failed = false
		if s.logf != nil {
			s.logf("listing running browsers for their debugging ports again")
		}
	}
}

func (s *liveScan) ports() ([]uint16, error) {
	now := time.Now()
	want := now.Add(-s.every)
	s.mu.Lock()
	s.touched = now
	if !s.busy {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	s.mu.Unlock()
	deadline := time.NewTimer(2 * s.wait)
	defer deadline.Stop()
	for {
		s.mu.Lock()
		if !s.okAt.IsZero() && !s.okAt.Before(want) {
			proc := s.proc
			s.mu.Unlock()
			return union(ActivePorts(s.o), proc), nil
		}
		r := s.startLocked()
		s.mu.Unlock()
		select {
		case <-r.done:
			if r.err != nil && !r.start.Before(want) {
				return nil, errNoProcessList
			}
		case <-deadline.C:
			return nil, errNoProcessList
		case <-s.ctx.Done():
			return nil, errNoProcessList
		}
	}
}

func (s *liveScan) loop() {
	t := time.NewTimer(s.idle)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-t.C:
		}
		s.mu.Lock()
		s.busy = time.Since(s.touched) < s.active
		r := s.startLocked()
		s.mu.Unlock()
		select {
		case <-s.ctx.Done():
			return
		case <-r.done:
		}
		s.mu.Lock()
		next := s.idle
		if s.busy = time.Since(s.touched) < s.active; s.busy {
			next = max(time.Until(r.start.Add(s.every-s.took-s.every/4)), 0)
		}
		s.mu.Unlock()
		t.Reset(next)
	}
}

func Guard(ctx context.Context, pol *policy.Policy, o Options, given, exposed []uint16, logf func(string, ...any)) error {
	ports, err := DebugPortRange()
	if err != nil {
		return err
	}
	scan := newLiveScan(ctx, o, logf)
	first := &scanRun{start: time.Now(), done: make(chan struct{})}
	scan.run = first
	scan.scan(first)
	found := union(ports, scan.snapshot())
	for _, p := range exposed {
		switch {
		case slices.Contains(given, p):
			return fmt.Errorf("--expose-debug-port %d: slicc drives that browser, so its port stays denied", p)
		case !slices.Contains(found, p):
			return fmt.Errorf("--expose-debug-port %d: not a debugging port (9222-9229, a DevToolsActivePort port, or a running browser's --remote-debugging-port)", p)
		}
	}
	report := func(p uint16) { logf("denied new debugging port %d on every address of this machine", p) }
	pol.Guard(policy.DebugPorts{Found: found, Given: given, Exposed: exposed})
	pol.Live(scan.ports, report)
	go scan.loop()
	every := watchEvery
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			for _, p := range scan.snapshot() {
				if pol.GuardPort(p) {
					report(p)
				}
			}
		}
	}()
	return nil
}
