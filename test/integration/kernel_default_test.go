package integration

import (
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/kernelin"
)

var kernelOn = regexp.MustCompile(`kernel services on http://<port>\.kernel\.localhost(?::(\d+))?/`)

func bindsPort80() bool {
	for _, addr := range []string{"127.0.0.1:80", "0.0.0.0:80"} {
		ln, err := net.Listen("tcp4", addr)
		if err == nil {
			_ = ln.Close()
			return true
		}
		if runtime.GOOS != "darwin" || !errors.Is(err, syscall.EACCES) {
			return false
		}
	}
	return false
}

func holdPort80(t *testing.T) {
	t.Helper()
	addr := "127.0.0.1:80"
	if runtime.GOOS == "darwin" {
		addr = "0.0.0.0:80"
	}
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		t.Logf("port 80 is out of reach already: %v", err)
		return
	}
	t.Cleanup(func() { _ = ln.Close() })
}

func startDefaultKernel(t *testing.T) *kernelEnv {
	t.Helper()
	b := launchBrowser(t)
	srv := pageServer(t)
	e := &kernelEnv{linkEnv: &linkEnv{t: t, b: b, origin: srv.URL}}
	e.echo = serveTCP(t, func(c net.Conn) { _, _ = io.Copy(c, c) })
	e.a = startAttach(t, "--cdp", b.endpoint, "--name", "it-node", "--origin", srv.URL)
	e.p = b.openPage(t, srv.URL+"/index.html?kernel")
	e.waitUp("")
	deadline := time.Now().Add(10 * time.Second)
	for {
		out := e.a.stderr.String()
		if m := kernelOn.FindStringSubmatch(out); m != nil {
			e.port = 80
			if m[1] != "" {
				e.port, _ = strconv.Atoi(m[1])
			}
			break
		}
		if time.Now().After(deadline) || strings.Contains(out, "kernel services are off") {
			t.Fatalf("kernel services never came up with the default port:\n%s", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.addr = fmt.Sprintf("127.0.0.1:%d", e.port)
	return e
}

func (e *kernelEnv) checkServes(t *testing.T) {
	t.Helper()
	host := fmt.Sprintf("5173.kernel.localhost:%d", e.port)
	if e.port == 80 {
		host = "5173.kernel.localhost"
	}
	if r := decodeReply(t, e.get(t, host, "/default")); r.Port != 5173 || r.Path != "/default" {
		t.Fatalf("kernel reply %+v", r)
	}
	var h struct {
		Kernel *struct {
			Port int `json:"port"`
		} `json:"kernel"`
	}
	e.p.eval("linkTest.ready()", &h)
	if h.Kernel == nil || h.Kernel.Port != e.port {
		t.Fatalf("HELLO kernel %+v, want port %d", h.Kernel, e.port)
	}
}

func nonLoopbackIPv4() net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			return n.IP
		}
	}
	return nil
}

func (e *kernelEnv) checkLoopbackOnly(t *testing.T) {
	t.Helper()
	ip := nonLoopbackIPv4()
	if ip == nil {
		t.Log("no non-loopback IPv4 address to check the peer filter with")
		return
	}
	c, err := net.DialTimeout("tcp4", net.JoinHostPort(ip.String(), strconv.Itoa(e.port)), 5*time.Second)
	if err != nil {
		return
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: 5173.kernel.localhost\r\n\r\n")
	b, _ := io.ReadAll(c)
	if len(b) > 0 {
		t.Fatalf("a peer on %s got an answer: %q", ip, b)
	}
	if !strings.Contains(e.a.stderr.String(), "not loopback") {
		t.Fatalf("stderr never said the peer on %s was refused:\n%s", ip, e.a.stderr.String())
	}
}

func TestKernelDefaultPort(t *testing.T) {
	t.Run("port 80 when this user can bind it", func(t *testing.T) {
		want80 := bindsPort80()
		e := startDefaultKernel(t)
		switch {
		case want80 && e.port != 80:
			t.Fatalf("kernel services on port %d, but this user can bind 80:\n%s", e.port, e.a.stderr.String())
		case !want80 && (e.port < kernelin.SpareFirst || e.port > kernelin.SpareLast):
			t.Fatalf("kernel services on port %d, want %d-%d", e.port, kernelin.SpareFirst, kernelin.SpareLast)
		}
		e.checkServes(t)
		if e.port == 80 {
			e.checkLoopbackOnly(t)
		}
	})
	t.Run("a spare port when 80 is taken", func(t *testing.T) {
		holdPort80(t)
		e := startDefaultKernel(t)
		if e.port < kernelin.SpareFirst || e.port > kernelin.SpareLast {
			t.Fatalf("kernel services on port %d, want %d-%d", e.port, kernelin.SpareFirst, kernelin.SpareLast)
		}
		e.a.waitStderr(t, fmt.Sprintf("so kernel services use port %d", e.port), time.Second)
		e.checkServes(t)
	})
}
