package sshd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type evalHost struct {
	argv   []string
	quiet  time.Duration
	dir    string
	mu     sync.Mutex
	repl   atomic.Pointer[repl]
	closed atomic.Bool
}

type evalEvent struct {
	stderr bool
	data   []byte
}

type repl struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan evalEvent
	exited chan struct{}
	status exitStatus
}

func (s *session) evalLoop() {
	host := s.srv.eval
	if host == nil {
		s.fail(1, "slicc: this machine has no REPL for %s", EvalSubsystem)
		return
	}
	go func() {
		in := bufio.NewReader(s.ch)
		for {
			batch, err := readBatch(in)
			if batch != "" {
				if everr := host.eval(s.ctx, batch, s.ch, s.ch.Stderr(), s.signals); everr != nil {
					s.fail(1, "%s: %v", EvalSubsystem, everr)
					return
				}
			}
			if err != nil {
				s.exit(exitStatus{})
				return
			}
		}
	}()
}

func readBatch(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return line, err
	}
	var b strings.Builder
	b.WriteString(line)
	for r.Buffered() > 0 {
		peek, _ := r.Peek(r.Buffered())
		if !bytes.Contains(peek, []byte{'\n'}) {
			break
		}
		next, err := r.ReadString('\n')
		b.WriteString(next)
		if err != nil {
			return b.String(), err
		}
	}
	return b.String(), nil
}

func (h *evalHost) eval(ctx context.Context, command string, stdout, stderr io.Writer, signals <-chan string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.repl.Load()
	if r == nil || r.dead() {
		var err error
		if r, err = startREPL(h.argv, h.dir); err != nil {
			return fmt.Errorf("cannot start %s: %w", h.argv[0], err)
		}
		h.repl.Store(r)
		if h.closed.Load() {
			r.kill()
			return errors.New("the server is shutting down")
		}
	}
	r.drain(stdout, stderr)
	if !strings.HasSuffix(command, "\n") {
		command += "\n"
	}
	if _, err := io.WriteString(r.stdin, command); err != nil {
		<-r.exited
		return r.deadErr()
	}
	return r.collect(ctx, h.quiet, stdout, stderr, signals)
}

func (h *evalHost) close() {
	h.closed.Store(true)
	if r := h.repl.Load(); r != nil {
		r.kill()
	}
}

func (r *repl) kill() {
	signalGroup(r.cmd.Process, "KILL")
	_ = r.stdin.Close()
}

func startREPL(argv []string, dir string) (*repl, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = dir, os.Environ()
	setProcAttr(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r := &repl{cmd: cmd, stdin: stdin, events: make(chan evalEvent, 64), exited: make(chan struct{})}
	var wg sync.WaitGroup
	wg.Add(2)
	go r.pump(stdout, false, &wg)
	go r.pump(stderr, true, &wg)
	go func() {
		wg.Wait()
		close(r.events)
		err := cmd.Wait()
		r.status = exitFrom(cmd.ProcessState, err)
		close(r.exited)
	}()
	return r, nil
}

func (r *repl) pump(src io.Reader, stderr bool, wg *sync.WaitGroup) {
	defer wg.Done()
	buf := make([]byte, 16*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			r.events <- evalEvent{stderr: stderr, data: bytes.Clone(buf[:n])}
		}
		if err != nil {
			return
		}
	}
}

func (r *repl) dead() bool {
	select {
	case <-r.exited:
		return true
	default:
		return false
	}
}

func (r *repl) deadErr() error {
	if r.status.signal != "" {
		return fmt.Errorf("the REPL was killed by SIG%s; the next session starts a new one", r.status.signal)
	}
	return fmt.Errorf("the REPL exited with code %d; the next session starts a new one", r.status.code)
}

func (r *repl) forward(ev evalEvent, stdout, stderr io.Writer) {
	if ev.stderr {
		_, _ = stderr.Write(ev.data)
	} else {
		_, _ = stdout.Write(ev.data)
	}
}

func (r *repl) drain(stdout, stderr io.Writer) {
	for {
		select {
		case ev, ok := <-r.events:
			if !ok {
				return
			}
			r.forward(ev, stdout, stderr)
		default:
			return
		}
	}
}

func (r *repl) collect(ctx context.Context, quiet time.Duration, stdout, stderr io.Writer, signals <-chan string) error {
	timer := time.NewTimer(quiet)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-r.events:
			if !ok {
				<-r.exited
				return r.deadErr()
			}
			r.forward(ev, stdout, stderr)
			timer.Reset(quiet)
		case <-timer.C:
			return nil
		case <-ctx.Done():
			interrupt(r.cmd.Process)
			return ctx.Err()
		case name := <-signals:
			if name == "INT" {
				interrupt(r.cmd.Process)
				continue
			}
			signalGroup(r.cmd.Process, name)
		}
	}
}
