//go:build windows

package browser

import (
	"errors"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func lockHeld(dir string) bool {
	name, err := windows.UTF16PtrFromString(filepath.Join(dir, "lockfile"))
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err == nil {
		_ = windows.CloseHandle(h)
		return false
	}
	return !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PATH_NOT_FOUND)
}
