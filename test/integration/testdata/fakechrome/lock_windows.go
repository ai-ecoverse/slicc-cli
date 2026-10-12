//go:build windows

package main

import (
	"os"
	"path/filepath"
)

func hold(dir string) func() {
	f, err := os.OpenFile(filepath.Join(dir, "lockfile"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return func() {}
	}
	return func() { _ = f.Close() }
}
