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

func ProcessPorts(ctx context.Context, o Options) []uint16 {
	table := o.Processes
	if table == nil {
		table = processTable
	}
	out, err := table(ctx)
	if err != nil {
		return nil
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
	return ports
}

type liveScan struct {
	ctx    context.Context
	o      Options
	mu     sync.Mutex
	procAt time.Time
	proc   []uint16
}

func (s *liveScan) ports() []uint16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.procAt.IsZero() || time.Since(s.procAt) >= processEvery {
		s.proc = ProcessPorts(s.ctx, s.o)
		s.procAt = time.Now()
	}
	return union(ActivePorts(s.o), s.proc)
}

func Guard(ctx context.Context, pol *policy.Policy, o Options, given, exposed []uint16, logf func(string, ...any)) error {
	scan := &liveScan{ctx: ctx, o: o}
	ports, err := DebugPortRange()
	if err != nil {
		return err
	}
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
	go func() {
		t := time.NewTicker(watchEvery)
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
