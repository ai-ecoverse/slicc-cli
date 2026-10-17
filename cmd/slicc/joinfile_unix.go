//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

func privateFile(path string, fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to another user", path)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s holds a secret, so others mustn't read it: chmod 600 %s", path, path)
	}
	return nil
}
