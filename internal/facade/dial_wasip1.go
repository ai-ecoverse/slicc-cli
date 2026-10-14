//go:build wasip1

package facade

import (
	"context"
	"errors"
	"io"
	"net"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

const (
	hostMagic  = 0x736c6331
	pollMillis = 100
)

var ErrNoHost = errors.New("this runtime has no slicc_host module (it needs slicc-kernel with @ai-ecoverse/wasi-slicc's host/slicc-host.mjs)")

//go:wasmimport wasi_snapshot_preview1 sched_yield
func schedYield() uint32

//go:wasmimport slicc_host probe
func hostProbe() uint32

//go:wasmimport slicc_host connect
func hostConnect(host unsafe.Pointer, size uint32, port uint32) int32

//go:wasmimport slicc_host connected
func hostConnected(fd int32, timeoutMs int32) int32

//go:wasmimport slicc_host read
func hostRead(fd int32, buf unsafe.Pointer, size uint32, timeoutMs int32) int32

//go:wasmimport slicc_host write
func hostWrite(fd int32, buf unsafe.Pointer, size uint32, timeoutMs int32) int32

//go:wasmimport slicc_host close
func hostClose(fd int32) int32

var available = sync.OnceValue(func() bool { return hostProbe() == hostMagic })

func errnoOf(n int32) error {
	return syscall.Errno(uint32(-n))
}

func retry(n int32) bool {
	e := syscall.Errno(uint32(-n))
	return e == syscall.EAGAIN || e == syscall.EINTR
}

func yield() {
	schedYield()
	runtime.Gosched()
}

func Dial(ctx context.Context, address string) (Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !available() {
		return nil, ErrNoHost
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 || host == "" {
		return nil, ErrBadURL
	}
	b := []byte(host)
	fd := hostConnect(unsafe.Pointer(unsafe.SliceData(b)), uint32(len(b)), uint32(port))
	runtime.KeepAlive(b)
	if fd < 0 {
		return nil, errnoOf(fd)
	}
	for {
		if err := ctx.Err(); err != nil {
			hostClose(fd)
			return nil, err
		}
		n := hostConnected(fd, pollMillis)
		switch {
		case n == 0:
			return &hostConn{fd: fd}, nil
		case retry(n):
			yield()
		default:
			hostClose(fd)
			return nil, errnoOf(n)
		}
	}
}

type hostConn struct {
	mu      sync.Mutex
	fd      int32
	closed  bool
	writing atomic.Int32
}

func (c *hostConn) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return 0, net.ErrClosed
		}
		wait := int32(pollMillis)
		if c.writing.Load() > 0 {
			wait = 0
		}
		n := hostRead(c.fd, unsafe.Pointer(unsafe.SliceData(b)), uint32(len(b)), wait)
		c.mu.Unlock()
		switch {
		case n > 0:
			return int(n), nil
		case n == 0:
			return 0, io.EOF
		case retry(n):
			yield()
		default:
			return 0, errnoOf(n)
		}
	}
}

func (c *hostConn) Write(b []byte) (int, error) {
	c.writing.Add(1)
	defer c.writing.Add(-1)
	written := 0
	for written < len(b) {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return written, net.ErrClosed
		}
		rest := b[written:]
		n := hostWrite(c.fd, unsafe.Pointer(unsafe.SliceData(rest)), uint32(len(rest)), pollMillis)
		c.mu.Unlock()
		switch {
		case n > 0:
			written += int(n)
		case n == 0 || retry(n):
			yield()
		default:
			return written, errnoOf(n)
		}
	}
	return written, nil
}

func (c *hostConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if n := hostClose(c.fd); n < 0 {
		return errnoOf(n)
	}
	return nil
}

func Ready() error {
	if !available() {
		return ErrNoHost
	}
	return nil
}
