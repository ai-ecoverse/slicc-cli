package sshd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

type ptyRequest struct {
	Term   string
	Cols   uint32
	Rows   uint32
	Width  uint32
	Height uint32
	Modes  string
}

type windowChange struct {
	Cols   uint32
	Rows   uint32
	Width  uint32
	Height uint32
}

type envRequest struct {
	Name  string
	Value string
}

type stringRequest struct {
	Value string
}

type exitStatusMsg struct {
	Status uint32
}

type exitSignalMsg struct {
	Signal     string
	CoreDumped bool
	Message    string
	Lang       string
}

type exitStatus struct {
	code   int
	signal string
	core   bool
}

type process interface {
	Signal(name string)
	Resize(cols, rows uint32)
	Wait() exitStatus
}

type session struct {
	ctx     context.Context
	cancel  context.CancelFunc
	srv     *Server
	ch      ssh.Channel
	env     []string
	pty     *ptyRequest
	started bool
	signals chan string
	mu      sync.Mutex
	proc    process
	once    sync.Once
}

func newSession(ctx context.Context, srv *Server, ch ssh.Channel) *session {
	ctx, cancel := context.WithCancel(ctx)
	return &session{ctx: ctx, cancel: cancel, srv: srv, ch: ch, signals: make(chan string, 8)}
}

func (s *session) serve(reqs <-chan *ssh.Request) {
	for req := range reqs {
		ok, then := s.handle(req)
		if req.WantReply {
			_ = req.Reply(ok, nil)
		}
		if then != nil {
			then()
		}
	}
	s.cancel()
	if p := s.process(); p != nil {
		p.Signal("HUP")
	}
}

func (s *session) handle(req *ssh.Request) (bool, func()) {
	switch req.Type {
	case "env":
		var r envRequest
		if ssh.Unmarshal(req.Payload, &r) != nil || r.Name == "" || strings.Contains(r.Name, "=") {
			return false, nil
		}
		s.env = append(s.env, r.Name+"="+r.Value)
		return true, nil
	case "pty-req":
		var r ptyRequest
		if s.started || ssh.Unmarshal(req.Payload, &r) != nil {
			return false, nil
		}
		s.pty = &r
		return true, nil
	case "window-change":
		s.windowChange(req.Payload)
		return true, nil
	case "signal":
		var r stringRequest
		if ssh.Unmarshal(req.Payload, &r) == nil {
			s.signal(r.Value)
		}
		return true, nil
	case "exec", "shell", "subsystem":
		if s.started {
			return false, nil
		}
		return s.start(req)
	}
	return false, nil
}

func (s *session) start(req *ssh.Request) (bool, func()) {
	var r stringRequest
	if req.Type != "shell" && ssh.Unmarshal(req.Payload, &r) != nil {
		return false, nil
	}
	switch req.Type {
	case "exec":
		s.started = true
		return true, func() { s.run(ExecArgv(s.srv.cfg.Runner, r.Value)) }
	case "shell":
		s.started = true
		return true, s.shell
	}
	switch r.Value {
	case SFTPSubsystem:
		if s.srv.cfg.NoSFTP {
			return false, nil
		}
		s.started = true
		return true, s.sftp
	case EvalSubsystem:
		s.started = true
		return true, s.evalLoop
	}
	return false, nil
}

func (s *session) shell() {
	if len(s.srv.shell) == 0 {
		s.fail(1, "slicc: no interactive shell for the runner %q; run a command instead (ssh <host> <command>)", strings.Join(s.srv.cfg.Runner, " "))
		return
	}
	s.run(s.srv.shell)
}

func (s *session) run(argv []string) {
	var p process
	var err error
	if s.pty != nil {
		p, err = startPTY(argv, s.environ(), s.srv.cfg.Dir, s.ch, s.pty)
	} else {
		p, err = startPipes(argv, s.environ(), s.srv.cfg.Dir, s.ch)
	}
	if err != nil {
		s.fail(127, "slicc: cannot start %s: %v", argv[0], err)
		return
	}
	s.mu.Lock()
	s.proc = p
	s.mu.Unlock()
	go func() { s.exit(p.Wait()) }()
}

func (s *session) process() process {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proc
}

func (s *session) environ() []string {
	env := os.Environ()
	if s.pty != nil && s.pty.Term != "" {
		env = append(env, "TERM="+s.pty.Term)
	}
	return append(env, s.env...)
}

func (s *session) windowChange(payload []byte) {
	var r windowChange
	if ssh.Unmarshal(payload, &r) != nil {
		return
	}
	if p := s.process(); p != nil {
		p.Resize(r.Cols, r.Rows)
		return
	}
	if s.pty != nil {
		s.pty.Cols, s.pty.Rows = r.Cols, r.Rows
	}
}

func (s *session) signal(name string) {
	name = strings.TrimPrefix(name, "SIG")
	if p := s.process(); p != nil {
		p.Signal(name)
		return
	}
	select {
	case s.signals <- name:
	default:
	}
}

func (s *session) fail(code int, format string, args ...any) {
	newline := "\n"
	if s.pty != nil {
		newline = "\r\n"
	}
	fmt.Fprintf(s.ch.Stderr(), format+newline, args...)
	s.exit(exitStatus{code: code})
}

func (s *session) exit(st exitStatus) {
	s.once.Do(func() {
		_ = s.ch.CloseWrite()
		if st.signal != "" {
			_, _ = s.ch.SendRequest("exit-signal", false, ssh.Marshal(exitSignalMsg{Signal: st.signal, CoreDumped: st.core}))
		} else {
			_, _ = s.ch.SendRequest("exit-status", false, ssh.Marshal(exitStatusMsg{Status: uint32(st.code)}))
		}
		_ = s.ch.Close()
		s.cancel()
	})
}
