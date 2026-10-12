package integration

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func shortName(p string) string {
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return ""
	}
	buf := make([]uint16, 1024)
	n, err := windows.GetShortPathName(p16, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:n]))
}
