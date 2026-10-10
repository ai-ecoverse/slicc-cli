package fs

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"syscall"
)

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

var fixedMessages = map[string]string{
	"EACCES":    "path escapes the folder",
	"EBADF":     "bad file handle",
	"EBUSY":     "the folder root cannot be moved or removed",
	"EINVAL":    "invalid argument",
	"EISDIR":    "is a directory",
	"ENOTDIR":   "not a directory",
	"ENOTEMPTY": "directory not empty",
	"EROFS":     "read-only folder",
	"ESTALE":    "file changed since it was opened",
}

var systemMessages = map[string]string{
	"E2BIG":        "argument list too long",
	"EACCES":       "permission denied",
	"EAGAIN":       "resource temporarily unavailable",
	"EBADF":        "bad file descriptor",
	"EBUSY":        "resource busy or locked",
	"EDQUOT":       "disk quota exceeded",
	"EEXIST":       "file already exists",
	"EFAULT":       "bad address in system call argument",
	"EFBIG":        "file too large",
	"EINTR":        "interrupted system call",
	"EINVAL":       "invalid argument",
	"EIO":          "i/o error",
	"EISDIR":       "illegal operation on a directory",
	"ELOOP":        "too many symbolic links encountered",
	"EMFILE":       "too many open files",
	"EMLINK":       "too many links",
	"ENAMETOOLONG": "name too long",
	"ENFILE":       "file table overflow",
	"ENODEV":       "no such device",
	"ENOENT":       "no such file or directory",
	"ENOMEM":       "not enough memory",
	"ENOSPC":       "no space left on device",
	"ENOSYS":       "function not implemented",
	"ENOTDIR":      "not a directory",
	"ENOTEMPTY":    "directory not empty",
	"ENOTSUP":      "operation not supported on socket",
	"ENXIO":        "no such device or address",
	"EOPNOTSUPP":   "operation not supported on socket",
	"EOVERFLOW":    "value too large for defined data type",
	"EPERM":        "operation not permitted",
	"EROFS":        "read-only file system",
	"ESPIPE":       "invalid seek",
	"ESTALE":       "stale file handle",
	"ETXTBSY":      "text file is busy",
	"EXDEV":        "cross-device link not permitted",
}

var errnoName = regexp.MustCompile(`^E[A-Z0-9]+$`)

func fail(code string, message ...string) *Error {
	if len(message) > 0 {
		return &Error{Code: code, Message: message[0]}
	}
	if m, ok := fixedMessages[code]; ok {
		return &Error{Code: code, Message: m}
	}
	if m, ok := systemMessages[code]; ok {
		return &Error{Code: code, Message: m}
	}
	return &Error{Code: code, Message: "input/output error"}
}

func Errno(err error) (code, message string) {
	var fe *Error
	if errors.As(err, &fe) {
		return fe.Code, fe.Message
	}
	if errors.Is(err, os.ErrClosed) {
		return "EBADF", fixedMessages["EBADF"]
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		name := sysErrnoName(errno)
		if errnoName.MatchString(name) {
			if m, ok := systemMessages[name]; ok {
				return name, m
			}
			return name, strings.ToLower(errno.Error())
		}
	}
	return "EIO", "input/output error"
}

func Reason(err error) string {
	code, message := Errno(err)
	return code + ": " + message
}
