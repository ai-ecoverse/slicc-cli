//go:build windows

package main

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

func openExclusive(name string, mode uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, mode, windows.FILE_ATTRIBUTE_NORMAL, 0)
}

func probeShared(name string, d time.Duration) int {
	busy := 0
	for end := time.Now().Add(d); time.Now().Before(end); {
		h, err := openExclusive(name, windows.OPEN_EXISTING)
		switch {
		case err == nil:
			_ = windows.CloseHandle(h)
		case errors.Is(err, windows.ERROR_SHARING_VIOLATION):
			busy++
		default:
			return busy
		}
	}
	return busy
}

func writeExclusive(name string, data []byte) error {
	h, err := openExclusive(name, windows.CREATE_ALWAYS)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var n uint32
	return windows.WriteFile(h, data, &n, nil)
}
