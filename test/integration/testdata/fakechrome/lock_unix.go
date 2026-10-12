//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func hold(dir string) func() {
	lock := filepath.Join(dir, "SingletonLock")
	_ = os.Remove(lock)
	_ = os.Symlink(fmt.Sprintf("fakehost-%d", os.Getpid()), lock)
	return func() { _ = os.Remove(lock) }
}
