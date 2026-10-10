package kernelin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/tcp"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

var (
	errHeadTooLarge = errors.New("kernel: request head too large")
	kernelHost      = regexp.MustCompile(`(?i)^([1-9][0-9]{0,4})\.kernel\.localhost(?::([0-9]+))?$`)
	requestLine     = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+ [^\\s]+ HTTP/1\\.[01]$")
	headEnd         = []byte("\r\n\r\n")
)

var statusText = map[int]string{
	400: "Bad Request",
	421: "Misdirected Request",
	431: "Request Header Fields Too Large",
	502: "Bad Gateway",
	504: "Gateway Timeout",
}

type Head struct {
	Method string
	Host   string
}

func readHead(r io.Reader) ([]byte, int, error) {
	var buf []byte
	chunk := make([]byte, 16*1024)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			from := max(0, len(buf)-len(headEnd)+1)
			buf = append(buf, chunk[:n]...)
			if i := bytes.Index(buf[from:], headEnd); i >= 0 {
				end := from + i
				if end > HeadLimit {
					return buf, 0, errHeadTooLarge
				}
				return buf, end, nil
			}
			if len(buf) > HeadLimit {
				return buf, 0, errHeadTooLarge
			}
		}
		if err != nil {
			return buf, 0, err
		}
	}
}

func ParseHead(head []byte) (Head, bool) {
	lines := strings.Split(string(head), "\r\n")
	if !requestLine.MatchString(lines[0]) {
		return Head{}, false
	}
	var hosts []string
	for _, f := range lines[1:] {
		if len(f) >= 5 && strings.EqualFold(f[:5], "host:") {
			hosts = append(hosts, f)
		}
	}
	if len(hosts) != 1 {
		return Head{}, false
	}
	method, _, _ := strings.Cut(lines[0], " ")
	return Head{Method: method, Host: strings.TrimSpace(hosts[0][5:])}, true
}

func KernelPort(host string, listenPort int) (int, bool) {
	m := kernelHost.FindStringSubmatch(host)
	if m == nil {
		return 0, false
	}
	port, err := strconv.Atoi(m[1])
	if err != nil || port > 65535 {
		return 0, false
	}
	if m[2] != "" && m[2] != strconv.Itoa(listenPort) {
		return 0, false
	}
	return port, true
}

func answer(c net.Conn, status int, message string) {
	body := message + "\n"
	resp := strings.Join([]string{
		fmt.Sprintf("HTTP/1.1 %d %s", status, statusText[status]),
		"Content-Type: text/plain; charset=utf-8",
		"Content-Length: " + strconv.Itoa(len(body)),
		"Cache-Control: no-store",
		"X-Proxy-Error: 1",
		"Connection: close",
		"",
		body,
	}, "\r\n")
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, resp); err != nil {
		return
	}
	if tc, ok := c.(interface{ CloseWrite() error }); ok {
		_ = tc.CloseWrite()
	}
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	_, _ = io.CopyN(io.Discard, c, 1<<20)
}

func pipe(s *link.Stream, c net.Conn) {
	tcp.Pipe(s, c)
}
