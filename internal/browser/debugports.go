package browser

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

const ProtectedPortsEnv = "SLICC_PROTECTED_PORTS"

var watchEvery = 5 * time.Second

const (
	firstDebugPort = 9222
	lastDebugPort  = 9229
)

func DebugPortRange() (uint16, uint16, error) {
	v := strings.TrimSpace(os.Getenv(ProtectedPortsEnv))
	if v == "" {
		return firstDebugPort, lastDebugPort, nil
	}
	a, b, ok := strings.Cut(v, "-")
	if !ok {
		b = a
	}
	first, err1 := strconv.ParseUint(strings.TrimSpace(a), 10, 16)
	last, err2 := strconv.ParseUint(strings.TrimSpace(b), 10, 16)
	if err1 != nil || err2 != nil || first == 0 || last < first {
		return 0, 0, fmt.Errorf("%s=%q: want <first>-<last>", ProtectedPortsEnv, v)
	}
	return uint16(first), uint16(last), nil
}

func DebugPorts(o Options) ([]uint16, error) {
	first, last, err := DebugPortRange()
	if err != nil {
		return nil, err
	}
	var ports []uint16
	for p := int(first); p <= int(last); p++ {
		ports = append(ports, uint16(p))
	}
	for _, p := range ActivePorts(o) {
		if p < first || p > last {
			ports = append(ports, p)
		}
	}
	return ports, nil
}

func WatchDebugPorts(ctx context.Context, o Options, every time.Duration, found func(uint16)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, p := range ActivePorts(o) {
			found(p)
		}
	}
}

func Guard(ctx context.Context, pol *policy.Policy, o Options, given, exposed []uint16, logf func(string, ...any)) error {
	found, err := DebugPorts(o)
	if err != nil {
		return err
	}
	pol.Guard(policy.DebugPorts{Found: found, Given: given, Exposed: exposed})
	go WatchDebugPorts(ctx, o, watchEvery, func(p uint16) {
		if pol.GuardPort(p) {
			logf("denied new DevToolsActivePort port %d on every address of this machine", p)
		}
	})
	return nil
}
