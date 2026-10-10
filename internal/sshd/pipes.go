package sshd

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

type pump struct {
	done chan struct{}
	busy atomic.Bool
	last atomic.Int64
}

func startPump(dst io.Writer, src io.ReadCloser) *pump {
	p := &pump{done: make(chan struct{})}
	p.touch()
	go p.run(dst, src)
	return p
}

func (p *pump) touch() {
	p.last.Store(time.Now().UnixNano())
}

func (p *pump) run(dst io.Writer, src io.ReadCloser) {
	defer close(p.done)
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			p.busy.Store(true)
			_, werr := dst.Write(buf[:n])
			p.busy.Store(false)
			p.touch()
			if werr != nil {
				_ = src.Close()
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *pump) wait(grace time.Duration) {
	p.touch()
	tick := time.NewTicker(grace / 8)
	defer tick.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-tick.C:
			idle := time.Since(time.Unix(0, p.last.Load()))
			if !p.busy.Load() && idle > grace {
				return
			}
		}
	}
}

type pipeProc struct {
	cmd    *exec.Cmd
	tree   *procTree
	stdin  *os.File
	stdout *os.File
	stderr *os.File
	out    *pump
	errOut *pump
	mu     sync.Mutex
	done   bool
}

func startPipes(argv, env []string, dir string, ch ssh.Channel) (process, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env, cmd.Dir = env, dir
	files, err := pipes()
	if err != nil {
		return nil, err
	}
	inR, inW, outR, outW, errR, errW := files[0], files[1], files[2], files[3], files[4], files[5]
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	tree, err := startTree(cmd)
	closeAll(inR, outW, errW)
	if err != nil {
		closeAll(inW, outR, errR)
		return nil, err
	}
	p := &pipeProc{cmd: cmd, tree: tree, stdin: inW, stdout: outR, stderr: errR}
	go func() {
		_, _ = io.Copy(inW, ch)
		_ = inW.Close()
	}()
	p.out = startPump(ch, outR)
	p.errOut = startPump(ch.Stderr(), errR)
	return p, nil
}

func pipes() ([]*os.File, error) {
	var files []*os.File
	for range 3 {
		r, w, err := os.Pipe()
		if err != nil {
			closeAll(files...)
			return nil, err
		}
		files = append(files, r, w)
	}
	return files, nil
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

func (p *pipeProc) Signal(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.done {
		p.tree.signal(name)
	}
}

func (p *pipeProc) Resize(uint32, uint32) {}

func (p *pipeProc) Wait() exitStatus {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.done = true
	p.mu.Unlock()
	_ = p.stdin.Close()
	p.out.wait(drainGrace)
	p.errOut.wait(drainGrace)
	p.tree.release()
	go closeAll(p.stdout, p.stderr)
	return exitFrom(p.cmd.ProcessState, err)
}
