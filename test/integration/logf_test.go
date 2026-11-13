package integration

import (
	"sync"
	"testing"
)

func testBoundLogf(t *testing.T) (func(string, ...any), func()) {
	t.Helper()
	var mu sync.Mutex
	done := false
	stop := func() {
		mu.Lock()
		done = true
		mu.Unlock()
	}
	t.Cleanup(stop)
	return func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		if done {
			return
		}
		t.Logf(format, args...)
	}, stop
}
