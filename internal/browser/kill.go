package browser

import (
	"context"
	"fmt"
	"time"
)

func stopRunning(ctx context.Context, in Installed) error {
	for range 5 {
		pid := findProcess(ctx, in.Executable)
		if pid <= 0 {
			return nil
		}
		signalTree(pid, 0, false)
		if waitPID(ctx, pid, 5*time.Second) {
			continue
		}
		signalTree(pid, 0, true)
		if !waitPID(ctx, pid, 3*time.Second) {
			return fmt.Errorf("%s (pid %d) did not exit", in.Name, pid)
		}
	}
	if findProcess(ctx, in.Executable) > 0 {
		return fmt.Errorf("%s keeps running", in.Name)
	}
	return nil
}

func waitPID(ctx context.Context, pid int, d time.Duration) bool {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for pidAlive(pid) {
		select {
		case <-ctx.Done():
			return !pidAlive(pid)
		case <-deadline.C:
			return !pidAlive(pid)
		case <-tick.C:
		}
	}
	return true
}
