//go:build !windows

package sshd

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

type ptyProc struct {
	cmd  *exec.Cmd
	ptmx *os.File
	tty  *os.File
	out  *pump
	mu   sync.Mutex
	done bool
}

func startPTY(argv, env []string, dir string, ch ssh.Channel, req *ptyRequest) (process, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env, cmd.Dir = env, dir
	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, err
	}
	if err := pty.Setsize(ptmx, winsize(req.Cols, req.Rows)); err != nil {
		closeAll(ptmx, tty)
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		closeAll(ptmx, tty)
		return nil, err
	}
	p := &ptyProc{cmd: cmd, ptmx: ptmx, tty: tty}
	go func() { _, _ = io.Copy(ptmx, ch) }()
	p.out = startPump(ch, ptmx)
	return p, nil
}

func winsize(cols, rows uint32) *pty.Winsize {
	if cols == 0 || rows == 0 {
		cols, rows = 80, 24
	}
	return &pty.Winsize{Cols: uint16(min(cols, 0xffff)), Rows: uint16(min(rows, 0xffff))}
}

func (p *ptyProc) Signal(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return
	}
	if name != "HUP" {
		if fg, err := unix.IoctlGetInt(int(p.ptmx.Fd()), unix.TIOCGPGRP); err == nil && fg > 0 {
			signalPgid(fg, name)
			return
		}
	}
	signalPgid(p.cmd.Process.Pid, name)
}

func (p *ptyProc) Resize(cols, rows uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.done {
		_ = pty.Setsize(p.ptmx, winsize(cols, rows))
	}
}

func (p *ptyProc) Wait() exitStatus {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.done = true
	p.mu.Unlock()
	_ = p.tty.Close()
	p.out.wait(drainGrace)
	go func() { _ = p.ptmx.Close() }()
	return exitFrom(p.cmd.ProcessState, err)
}
