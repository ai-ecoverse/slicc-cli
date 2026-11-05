package flow

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

func refused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED) || errors.Is(err, syscall.ECONNREFUSED)
}
