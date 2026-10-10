//go:build !windows

package browser

import (
	"strconv"
	"strings"
)

func matchProcess(psOutput, exe string) int {
	for _, line := range strings.Split(psOutput, "\n") {
		line = strings.TrimSpace(line)
		pidStr, args, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		args = strings.TrimSpace(args)
		if args != exe && !strings.HasPrefix(args, exe+" ") {
			continue
		}
		if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
			return pid
		}
	}
	return 0
}
