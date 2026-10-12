//go:build !windows

package main

import (
	"os"
	"time"
)

func probeShared(_ string, d time.Duration) int {
	time.Sleep(d)
	return 0
}

func writeExclusive(name string, data []byte) error {
	return os.WriteFile(name, data, 0o600)
}
