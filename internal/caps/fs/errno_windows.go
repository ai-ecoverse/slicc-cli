//go:build windows

package fs

import (
	"syscall"

	"golang.org/x/sys/windows"
)

var winErrnos = map[syscall.Errno]string{
	windows.ERROR_FILE_NOT_FOUND:        "ENOENT",
	windows.ERROR_PATH_NOT_FOUND:        "ENOENT",
	windows.ERROR_INVALID_NAME:          "ENOENT",
	windows.ERROR_INVALID_DRIVE:         "ENOENT",
	windows.ERROR_BAD_PATHNAME:          "ENOENT",
	windows.ERROR_BAD_NETPATH:           "ENOENT",
	windows.ERROR_DIRECTORY:             "ENOTDIR",
	windows.ERROR_ACCESS_DENIED:         "EPERM",
	windows.ERROR_PRIVILEGE_NOT_HELD:    "EPERM",
	windows.ERROR_CANT_ACCESS_FILE:      "EACCES",
	windows.ERROR_SHARING_VIOLATION:     "EBUSY",
	windows.ERROR_LOCK_VIOLATION:        "EBUSY",
	windows.ERROR_FILE_EXISTS:           "EEXIST",
	windows.ERROR_ALREADY_EXISTS:        "EEXIST",
	windows.ERROR_DIR_NOT_EMPTY:         "ENOTEMPTY",
	windows.ERROR_DISK_FULL:             "ENOSPC",
	windows.ERROR_HANDLE_DISK_FULL:      "ENOSPC",
	windows.ERROR_FILENAME_EXCED_RANGE:  "ENAMETOOLONG",
	windows.ERROR_INVALID_PARAMETER:     "EINVAL",
	windows.ERROR_NEGATIVE_SEEK:         "EINVAL",
	windows.ERROR_NOT_A_REPARSE_POINT:   "EINVAL",
	windows.ERROR_INVALID_HANDLE:        "EBADF",
	windows.ERROR_TOO_MANY_OPEN_FILES:   "EMFILE",
	windows.ERROR_NOT_SAME_DEVICE:       "EXDEV",
	windows.ERROR_WRITE_PROTECT:         "EROFS",
	windows.ERROR_CANT_RESOLVE_FILENAME: "ELOOP",
	windows.ERROR_STOPPED_ON_SYMLINK:    "ELOOP",
	windows.ERROR_NOT_SUPPORTED:         "ENOTSUP",
	windows.ERROR_FILE_TOO_LARGE:        "EFBIG",
	windows.ERROR_NOT_ENOUGH_MEMORY:     "ENOMEM",
	windows.ERROR_OUTOFMEMORY:           "ENOMEM",
	windows.ERROR_CURRENT_DIRECTORY:     "EBUSY",
	windows.ERROR_BUSY:                  "EBUSY",
	windows.ERROR_DIR_NOT_ROOT:          "ENOTEMPTY",
	windows.ERROR_NOT_READY:             "EAGAIN",
	windows.ERROR_IO_DEVICE:             "EIO",
	syscall.EINVAL:                      "EINVAL",
	syscall.EISDIR:                      "EISDIR",
	syscall.EEXIST:                      "EEXIST",
	syscall.ENOTEMPTY:                   "ENOTEMPTY",
	syscall.EPERM:                       "EPERM",
	syscall.EACCES:                      "EACCES",
	syscall.ELOOP:                       "ELOOP",
	syscall.EBADF:                       "EBADF",
	syscall.EROFS:                       "EROFS",
	syscall.ENAMETOOLONG:                "ENAMETOOLONG",
	syscall.EWINDOWS:                    "ENOSYS",
}

func sysErrnoName(e syscall.Errno) string { return winErrnos[e] }
