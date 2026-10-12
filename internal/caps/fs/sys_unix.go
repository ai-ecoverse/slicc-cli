//go:build !windows

package fs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	forbidden         = "\x00"
	symlinksSupported = true
	chmodSupported    = true
)

func realpath(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	if err != nil && strings.Contains(err.Error(), "too many links") {
		return "", syscall.ELOOP
	}
	return r, err
}

func isDirectory(string) error { return nil }

func longLeaf(p string) string { return p }

func u32[T ~uint16 | ~uint32](v T) uint32 { return uint32(v) }

func i64[T ~int32 | ~uint32 | ~int64](v T) int64 { return int64(v) }

func infoOf(st *unix.Stat_t) info {
	i := info{
		size:    st.Size,
		mtimeNs: st.Mtim.Nano(),
		atimeNs: st.Atim.Nano(),
		mode:    u32(st.Mode) & 0o7777,
		ino:     st.Ino,
		kind:    KindFile,
	}
	switch u32(st.Mode) & unix.S_IFMT {
	case unix.S_IFDIR:
		i.kind = KindDirectory
	case unix.S_IFLNK:
		i.kind = KindSymlink
	case unix.S_IFREG:
		i.regular = true
	}
	return i
}

func lstatInfo(p string) (info, error) {
	var st unix.Stat_t
	if err := unix.Lstat(p, &st); err != nil {
		return info{}, err
	}
	return infoOf(&st), nil
}

func fstatInfo(f *os.File) (info, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return info{}, err
	}
	return infoOf(&st), nil
}

func openNoFollow(p string, fl openFlags, mode uint32) (*os.File, error) {
	flags := unix.O_RDONLY
	if fl.write {
		flags = unix.O_RDWR
	}
	if fl.create {
		flags |= unix.O_CREAT
		if fl.exclusive {
			flags |= unix.O_EXCL
		}
	}
	if fl.truncate {
		flags |= unix.O_TRUNC
	}
	fd, err := unix.Open(p, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, mode)
	for errors.Is(err, unix.EINTR) {
		fd, err = unix.Open(p, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, mode)
	}
	if err != nil {
		return nil, err
	}
	_ = unix.SetNonblock(fd, false)
	return os.NewFile(uintptr(fd), p), nil
}

func removeFile(p string, _ info) error { return unix.Unlink(p) }

func removeDir(p string) error { return unix.Rmdir(p) }

func renamePath(from, to string) error { return unix.Rename(from, to) }

func makeDir(p string) error { return unix.Mkdir(p, 0o777) }

func makeSymlink(target, p string) error { return unix.Symlink(target, p) }

func changeMode(p string, mode uint32) error { return unix.Chmod(p, mode) }

func setMtime(p string, st info, mtimeNs int64) error {
	ts := []unix.Timespec{unix.NsecToTimespec(st.atimeNs), unix.NsecToTimespec(mtimeNs)}
	return unix.UtimesNanoAt(unix.AT_FDCWD, p, ts, unix.AT_SYMLINK_NOFOLLOW)
}

func statfsOf(root string) (Statfs, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(root, &st); err != nil {
		return Statfs{}, err
	}
	return Statfs{Bsize: i64(st.Bsize), Blocks: st.Blocks, Bfree: st.Bfree, Bavail: st.Bavail}, nil
}

func normalization() string {
	if runtime.GOOS == "darwin" {
		return "nfd-insensitive"
	}
	return "none"
}

func openDir(p string) (*os.File, error) {
	fd, err := unix.Open(p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), p), nil
}
