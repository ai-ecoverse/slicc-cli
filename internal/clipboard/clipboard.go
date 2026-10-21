package clipboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
	"unicode/utf8"
)

const (
	MaxBytes = 1 << 20
	Timeout  = 5 * time.Second
)

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func fail(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

type Clipboard struct {
	Name  string
	read  func(context.Context) ([]byte, error)
	write func(context.Context, []byte) error
}

func Find() (*Clipboard, string) { return find(os.Getenv, exec.LookPath) }

func (c *Clipboard) Read(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	b, err := c.read(ctx)
	if err != nil {
		return "", err
	}
	if len(b) > MaxBytes {
		return "", fail("EFBIG", "the clipboard holds more than 1 MiB of text")
	}
	if !utf8.Valid(b) {
		return "", fail("EILSEQ", "the clipboard's text isn't UTF-8")
	}
	return string(b), nil
}

func (c *Clipboard) Write(ctx context.Context, text []byte) error {
	if len(text) > MaxBytes {
		return fail("EFBIG", "the text is over 1 MiB")
	}
	if !utf8.Valid(text) {
		return fail("EILSEQ", "the text isn't UTF-8")
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	return c.write(ctx, text)
}
