package agentctl

import (
	"context"
	"encoding/json"
	"errors"
	"io"
)

type Link interface {
	Allowed() bool
	Name() string
	Open(ctx context.Context, open any) (Pipe, error)
	Done() <-chan struct{}
}

type Pipe interface {
	io.Reader
	io.Writer
	CloseWrite() error
	Reset(reason string) error
	Done() <-chan struct{}
	Opened() json.RawMessage
}

type ResetError struct {
	Code    string
	Message string
	Remote  bool
}

func (e *ResetError) Error() string {
	if e.Message != "" {
		return e.Message + " (" + e.Code + ")"
	}
	return e.Code
}

type RefusedError struct {
	Message string
}

func (e *RefusedError) Error() string { return e.Message }

func (e *RefusedError) Is(target error) bool { return target == ErrNotAllowed }

func Refusal(err error) string {
	var re *RefusedError
	if errors.As(err, &re) {
		return re.Message
	}
	return ""
}

func remoteCode(err error) (string, string, bool) {
	var re *ResetError
	if errors.As(err, &re) {
		return re.Code, re.Message, re.Remote
	}
	if code, ok := linkCode(err); ok {
		return code, "", true
	}
	return "", "", false
}
