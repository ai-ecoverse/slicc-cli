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

func primaryDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "slicc", "run"), nil
}

func fallbackDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".slicc-run"), nil
}

func Dir() (string, error) {
	dir, err := primaryDir()
	if err != nil {
		return "", err
	}
	if len(dir)+len(`\4294967295.sock`) > maxSockPath {
		return fallbackDir()
	}
	return dir, nil
}

func searchDirs() ([]string, error) {
	var dirs []string
	for _, f := range []func() (string, error){primaryDir, fallbackDir} {
		if dir, err := f(); err == nil {
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) == 0 {
		return nil, errors.New("no per-user directory")
	}
	return dirs, nil
}

func privateDACL() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)", nil
}

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	want, err := privateDACL()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(want)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func ownDir(dir string) bool {
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	want, err := privateDACL()
	if err != nil {
		return false
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	return err == nil && sd.String() == want
}

func refused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND)
}

func peerIsUs(net.Conn) bool { return true }
