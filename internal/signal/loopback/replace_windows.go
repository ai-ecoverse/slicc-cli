//go:build windows

package loopback

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

const shareRetry = 2 * time.Second

func sharingViolation(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

func retryShared(op func() error) error {
	deadline := time.Now().Add(shareRetry)
	for {
		err := op()
		if err == nil || !sharingViolation(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func replaceFile(from, to string) error {
	return retryShared(func() error { return os.Rename(from, to) })
}

func readShared(path string) ([]byte, error) {
	var data []byte
	err := retryShared(func() error {
		var err error
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}
