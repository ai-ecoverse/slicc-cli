//go:build unix

package ctl

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
)

func primaryDir() string {
	if runtime.GOOS == "linux" {
		if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" && filepath.IsAbs(x) {
			return filepath.Join(x, "slicc")
		}
	}
	return filepath.Join(os.TempDir(), "slicc-"+strconv.Itoa(os.Getuid()))
}

func fallbackDir() string { return "/tmp/slicc-" + strconv.Itoa(os.Getuid()) }

func Dir() (string, error) {
	dir := primaryDir()
	if len(dir)+len("/4294967295.sock") > maxSockPath {
		dir = fallbackDir()
	}
	return dir, nil
}

func searchDirs() ([]string, error) {
	return []string{primaryDir(), fallbackDir()}, nil
}

func ensureDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s isn't a directory", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to another user", dir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

func ownDir(dir string) bool {
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

func refused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, fs.ErrNotExist)
}
