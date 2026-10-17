package browser

import (
	"context"
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
	watchEvery   = 5 * time.Second
	processEvery = time.Second
)

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

type liveScan struct {
	ctx    context.Context
	o      Options
	logf   func(string, ...any)
	every  time.Duration
	mu     sync.Mutex
	proc   []uint16
	failed bool
}

func (s *liveScan) ports() []uint16 {
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	return union(ActivePorts(s.o), proc)
}

func (s *liveScan) refresh() {
	ports, err := ProcessPorts(s.ctx, s.o)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if !s.failed {
			s.failed = true
			if s.logf != nil {
				s.logf("couldn't list running browsers for their debugging ports: %v", err)
			}
		}
		return
	}
	was := s.failed
	s.failed = false
	s.proc = ports
	if was && s.logf != nil {
		s.logf("listing running browsers for their debugging ports again")
	}
}

func (s *liveScan) loop() {
	every := s.every
	if every <= 0 {
		every = processEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.refresh()
		}
	}
}

func Guard(ctx context.Context, pol *policy.Policy, o Options, given, exposed []uint16, logf func(string, ...any)) error {
	ports, err := DebugPortRange()
	if err != nil {
		return err
	}
	procEvery, fileEvery := processEvery, watchEvery
	scan := &liveScan{ctx: ctx, o: o, logf: logf, every: procEvery}
	scan.refresh()
	found := union(ports, scan.ports())
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
	go func() {
		t := time.NewTicker(fileEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			for _, p := range scan.ports() {
				if pol.GuardPort(p) {
					report(p)
				}
			}
		}
	}()
	return nil
}
