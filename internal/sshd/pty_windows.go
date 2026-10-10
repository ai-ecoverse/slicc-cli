//go:build windows

package sshd

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/charmbracelet/x/conpty"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/windows"
)

type conProc struct {
	cpty   *conpty.ConPty
	tree   *procTree
	handle windows.Handle
	output *os.File
	out    *pump
	mu     sync.Mutex
	done   bool
}

func startPTY(argv, env []string, dir string, ch ssh.Channel, req *ptyRequest) (process, error) {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, err
	}
	cols, rows := req.Cols, req.Rows
	if cols == 0 || rows == 0 {
		cols, rows = 80, 24
	}
	cpty, err := conpty.New(int(min(cols, 0x7fff)), int(min(rows, 0x7fff)), 0)
	if err != nil {
		return nil, err
	}
	output, err := dupReader(cpty.OutPipeReadFd())
	if err != nil {
		_ = cpty.Close()
		return nil, err
	}
	attr := &syscall.ProcAttr{Dir: dir, Env: env, Sys: &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}}
	pid, handle, err := cpty.Spawn(path, argv, attr)
	if err != nil {
		_ = output.Close()
		_ = cpty.Close()
		return nil, err
	}
	tree, err := adoptSuspended(windows.Handle(handle), pid)
	if err != nil {
		_ = windows.TerminateProcess(windows.Handle(handle), 1)
		_ = windows.CloseHandle(windows.Handle(handle))
		_ = output.Close()
		_ = cpty.Close()
		return nil, err
	}
	p := &conProc{cpty: cpty, tree: tree, handle: windows.Handle(handle), output: output}
	go func() { _, _ = io.Copy(cpty, ch) }()
	p.out = startPump(ch, output)
	return p, nil
}

func dupReader(fd uintptr) (*os.File, error) {
	self := windows.CurrentProcess()
	var dup windows.Handle
	if err := windows.DuplicateHandle(self, windows.Handle(fd), self, &dup, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(dup), "conpty"), nil
}

func (p *conProc) Signal(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return
	}
	if name == "INT" {
		_, _ = p.cpty.Write([]byte{3})
		return
	}
	p.tree.signal(name)
}

func (p *conProc) Resize(cols, rows uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.done && cols > 0 && rows > 0 {
		_ = p.cpty.Resize(int(min(cols, 0x7fff)), int(min(rows, 0x7fff)))
	}
}

func (p *conProc) Wait() exitStatus {
	_, _ = windows.WaitForSingleObject(p.handle, windows.INFINITE)
	var code uint32
	if err := windows.GetExitCodeProcess(p.handle, &code); err != nil {
		code = 1
	}
	p.mu.Lock()
	p.done = true
	p.mu.Unlock()
	_ = windows.CloseHandle(p.handle)
	go func() { _ = p.cpty.Close() }()
	p.out.wait(drainGrace)
	p.tree.release()
	go func() { _ = p.output.Close() }()
	return exitStatus{code: int(code)}
}
