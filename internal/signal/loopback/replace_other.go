//go:build !windows

package loopback

import "os"

func replaceFile(from, to string) error { return os.Rename(from, to) }

func readShared(path string) ([]byte, error) { return os.ReadFile(path) }
