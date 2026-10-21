//go:build unix

package clipboard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"
)

const maxStderr = 4096

type tool struct {
	name    string
	path    string
	args    []string
	env     []string
	empty   []string
	capture bool
}

var deniedWords = []string{"permission denied", "not authorized", "access denied", "operation not permitted"}

func (t tool) command(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, t.path, t.args...)
	cmd.Env = append(os.Environ(), t.env...)
	cmd.WaitDelay = time.Second
	return cmd
}

func (t tool) startErr(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fail("ENOTSUP", "%s is gone", t.name)
	case errors.Is(err, fs.ErrPermission):
		return fail("EACCES", "%s can't be run", t.name)
	}
	return fail("EIO", "%s couldn't start", t.name)
}

func (t tool) exitErr(ctx context.Context, err error, stderr string) error {
	if ctx.Err() != nil {
		return fail("EIO", "%s timed out", t.name)
	}
	low := strings.ToLower(stderr)
	for _, w := range deniedWords {
		if strings.Contains(low, w) {
			return fail("EACCES", "%s was refused", t.name)
		}
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() >= 0 {
		return fail("EIO", "%s exited %d", t.name, ee.ExitCode())
	}
	return fail("EIO", "%s failed", t.name)
}

func (t tool) readText(ctx context.Context) ([]byte, error) {
	cmd := t.command(ctx)
	out := &capped{kill: func() { _ = cmd.Process.Kill() }}
	var stderr limited
	cmd.Stdout, cmd.Stderr = out, &stderr
	if err := cmd.Start(); err != nil {
		return nil, t.startErr(err)
	}
	err := cmd.Wait()
	if out.full {
		return out.b.Bytes(), nil
	}
	if err != nil {
		for _, e := range t.empty {
			if strings.Contains(stderr.String(), e) {
				return nil, nil
			}
		}
		return nil, t.exitErr(ctx, err, stderr.String())
	}
	return out.b.Bytes(), nil
}

type capped struct {
	b    bytes.Buffer
	full bool
	kill func()
}

func (c *capped) Write(p []byte) (int, error) {
	if room := MaxBytes + 1 - c.b.Len(); len(p) >= room {
		c.b.Write(p[:room])
		c.full = true
		c.kill()
		return 0, io.ErrShortWrite
	}
	return c.b.Write(p)
}

func (t tool) writeText(ctx context.Context, text []byte) error {
	cmd := t.command(ctx)
	cmd.Stdin = bytes.NewReader(text)
	var stderr limited
	if t.capture {
		cmd.Stderr = &stderr
	}
	if err := cmd.Start(); err != nil {
		return t.startErr(err)
	}
	if err := cmd.Wait(); err != nil {
		return t.exitErr(ctx, err, stderr.String())
	}
	return nil
}

type limited struct{ b bytes.Buffer }

func (l *limited) Write(p []byte) (int, error) {
	if room := maxStderr - l.b.Len(); room > 0 {
		l.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (l *limited) String() string { return l.b.String() }
