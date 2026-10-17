//go:build windows

package ctl

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func Dir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "slicc", "run"), nil
}

func searchDirs() ([]string, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return []string{dir}, nil
}

func ensureDir(dir string) error { return os.MkdirAll(dir, 0o700) }

func ownDir(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

func refused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND)
}

func peerIsUs(net.Conn) bool { return true }
