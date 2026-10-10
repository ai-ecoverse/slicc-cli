//go:build windows

package fs

import (
	"errors"
	"io"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	forbidden         = "\x00\\:"
	symlinksSupported = false
	chmodSupported    = false
	shareAll          = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE
)

func createFile(p string, access, disposition, flags uint32) (windows.Handle, error) {
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return windows.InvalidHandle, fail("EINVAL")
	}
	return windows.CreateFile(p16, access, shareAll, nil, disposition, flags|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
}

func realpath(p string) (string, error) {
	h, err := createFile(p, 0, windows.OPEN_EXISTING, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	buf := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n+1)
	}
}

func isDirectory(p string) error {
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return fail("EINVAL")
	}
	attrs, err := windows.GetFileAttributes(p16)
	if err != nil {
		return err
	}
	if attrs&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fail("ENOTDIR")
	}
	return nil
}

type attributeTagInfo struct {
	FileAttributes uint32
	ReparseTag     uint32
}

func kindOf(attrs, tag uint32) (string, bool) {
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && (tag == windows.IO_REPARSE_TAG_SYMLINK || tag == windows.IO_REPARSE_TAG_MOUNT_POINT) {
		return KindSymlink, false
	}
	if attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return KindDirectory, false
	}
	return KindFile, true
}

func modeOf(attrs uint32, kind string) uint32 {
	mode := uint32(0o666)
	if attrs&windows.FILE_ATTRIBUTE_READONLY != 0 {
		mode = 0o444
	}
	if kind == KindDirectory {
		mode |= 0o111
	}
	return mode
}

func handleInfo(h windows.Handle) (info, error) {
	var d windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &d); err != nil {
		return info{}, err
	}
	var tag attributeTagInfo
	if d.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		if err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&tag)), uint32(unsafe.Sizeof(tag))); err != nil {
			return info{}, err
		}
	}
	kind, regular := kindOf(d.FileAttributes, tag.ReparseTag)
	return info{
		kind:    kind,
		regular: regular,
		size:    int64(d.FileSizeHigh)<<32 | int64(d.FileSizeLow),
		mtimeNs: d.LastWriteTime.Nanoseconds(),
		atimeNs: d.LastAccessTime.Nanoseconds(),
		mode:    modeOf(d.FileAttributes, kind),
		ino:     uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow),
	}, nil
}

func lstatInfo(p string) (info, error) {
	h, err := createFile(p, windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if err == nil {
		defer func() { _ = windows.CloseHandle(h) }()
		return handleInfo(h)
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return info{}, err
	}
	fi, serr := os.Lstat(p)
	if serr != nil {
		return info{}, err
	}
	d, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return info{}, err
	}
	var tag uint32
	if fi.Mode()&os.ModeSymlink != 0 {
		tag = windows.IO_REPARSE_TAG_SYMLINK
	}
	kind, regular := kindOf(d.FileAttributes, tag)
	return info{
		kind:    kind,
		regular: regular,
		size:    int64(d.FileSizeHigh)<<32 | int64(d.FileSizeLow),
		mtimeNs: d.LastWriteTime.Nanoseconds(),
		atimeNs: d.LastAccessTime.Nanoseconds(),
		mode:    modeOf(d.FileAttributes, kind),
	}, nil
}

func fstatInfo(f *os.File) (info, error) { return handleInfo(windows.Handle(f.Fd())) }

func openNoFollow(p string, fl openFlags, mode uint32) (*os.File, error) {
	access := uint32(windows.GENERIC_READ)
	if fl.write {
		access |= windows.GENERIC_WRITE
	}
	disposition := uint32(windows.OPEN_EXISTING)
	switch {
	case fl.create && fl.exclusive:
		disposition = windows.CREATE_NEW
	case fl.create:
		disposition = windows.OPEN_ALWAYS
	}
	attrs := uint32(windows.FILE_ATTRIBUTE_NORMAL)
	if fl.create && mode&0o200 == 0 {
		attrs = windows.FILE_ATTRIBUTE_READONLY
	}
	h, err := createFile(p, access, disposition, attrs|windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if err != nil {
		return nil, err
	}
	st, err := handleInfo(h)
	if err == nil && st.kind == KindSymlink {
		err = fail("ELOOP", "is a symlink")
	}
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	f := os.NewFile(uintptr(h), p)
	if fl.truncate && st.regular {
		if err := f.Truncate(0); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return f, nil
}

func openDir(p string) (*os.File, error) { return os.Open(p) }

func removeFile(p string, st info) error {
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return fail("EINVAL")
	}
	if st.kind == KindSymlink {
		if attrs, err := windows.GetFileAttributes(p16); err == nil && attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
			return windows.RemoveDirectory(p16)
		}
	}
	err = windows.DeleteFile(p16)
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return err
	}
	attrs, aerr := windows.GetFileAttributes(p16)
	if aerr != nil || attrs&windows.FILE_ATTRIBUTE_READONLY == 0 {
		return err
	}
	if windows.SetFileAttributes(p16, attrs&^windows.FILE_ATTRIBUTE_READONLY) != nil {
		return err
	}
	if err = windows.DeleteFile(p16); err != nil {
		_ = windows.SetFileAttributes(p16, attrs)
	}
	return err
}

func removeDir(p string) error {
	st, err := lstatInfo(p)
	if err != nil {
		return err
	}
	if st.kind != KindDirectory {
		return fail("ENOTDIR")
	}
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return fail("EINVAL")
	}
	return windows.RemoveDirectory(p16)
}

func emptyDir(p string) (bool, error) {
	d, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer func() { _ = d.Close() }()
	names, err := d.Readdirnames(1)
	if len(names) > 0 {
		return false, nil
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return true, nil
}

func renamePath(from, to string) error {
	src, err := lstatInfo(from)
	if err != nil {
		return err
	}
	dst, err := lstatInfo(to)
	if err == nil && dst.ino != src.ino {
		srcDir, dstDir := src.kind == KindDirectory, dst.kind == KindDirectory
		switch {
		case srcDir && !dstDir:
			return fail("ENOTDIR")
		case !srcDir && dstDir:
			return fail("EISDIR")
		case srcDir:
			empty, err := emptyDir(to)
			if err != nil {
				return err
			}
			if !empty {
				return fail("ENOTEMPTY")
			}
			if err := removeDir(to); err != nil {
				return err
			}
		}
	}
	f16, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return fail("EINVAL")
	}
	t16, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return fail("EINVAL")
	}
	return windows.MoveFileEx(f16, t16, windows.MOVEFILE_REPLACE_EXISTING)
}

func makeDir(p string) error { return os.Mkdir(p, 0o777) }

func makeSymlink(string, string) error {
	return fail("EPERM", "symlinks are not supported on this host")
}

func changeMode(p string, mode uint32) error { return os.Chmod(p, os.FileMode(mode)) }

func setMtime(p string, _ info, mtimeNs int64) error {
	h, err := createFile(p, windows.FILE_WRITE_ATTRIBUTES, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	ft := windows.NsecToFiletime(mtimeNs)
	return windows.SetFileTime(h, nil, nil, &ft)
}

var procGetDiskFreeSpace = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetDiskFreeSpaceW")

func clusterSize(root string) int64 {
	buf := make([]uint16, 512)
	r16, err := windows.UTF16PtrFromString(root)
	if err != nil || windows.GetVolumePathName(r16, &buf[0], uint32(len(buf))) != nil {
		return 4096
	}
	var spc, bps, freeClusters, totalClusters uint32
	ok, _, _ := procGetDiskFreeSpace.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&spc)), uintptr(unsafe.Pointer(&bps)), uintptr(unsafe.Pointer(&freeClusters)), uintptr(unsafe.Pointer(&totalClusters)))
	if ok == 0 || spc == 0 || bps == 0 {
		return 4096
	}
	return int64(spc) * int64(bps)
}

func statfsOf(root string) (Statfs, error) {
	r16, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return Statfs{}, fail("EINVAL")
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(r16, &avail, &total, &free); err != nil {
		return Statfs{}, err
	}
	bsize := clusterSize(root)
	b := uint64(bsize)
	return Statfs{Bsize: bsize, Blocks: total / b, Bfree: free / b, Bavail: avail / b}, nil
}

func normalization() string { return "none" }
