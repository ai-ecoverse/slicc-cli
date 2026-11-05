//go:build !windows

package flow

import (
	"errors"
	"syscall"
)

func refused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
