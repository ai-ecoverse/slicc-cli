package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	dir := ""
	for _, a := range os.Args[1:] {
		if v, ok := strings.CutPrefix(a, "--user-data-dir="); ok {
			dir = v
		}
	}
	saw, _ := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
	if log := os.Getenv("FAKE_CHROME_LOG"); log != "" {
		b, _ := json.Marshal(map[string]any{"args": os.Args[1:], "sawActivePort": string(saw)})
		_ = os.WriteFile(log, b, 0o600)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	release := hold(dir)
	port := l.Addr().(*net.TCPAddr).Port
	ws := "/devtools/browser/fake-" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(fmt.Sprintf("%d\n%s", port, ws)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/154.0.8037.98",
			"User-Agent":           "Mozilla/5.0 Fake Chrome/154.0.8037.98 Safari/537.36",
			"webSocketDebuggerUrl": "ws://" + r.Host + ws,
		})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "[]")
	})
	mux.HandleFunc(ws, func(w http.ResponseWriter, r *http.Request) {
		if acceptOne(w, r) == "Browser.close" {
			release()
			os.Exit(0)
		}
	})
	_ = http.Serve(l, mux)
}

func acceptOne(w http.ResponseWriter, r *http.Request) string {
	h := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		base64.StdEncoding.EncodeToString(h[:]) + "\r\n\r\n")
	_ = rw.Flush()
	payload := readFrame(rw.Reader)
	var msg struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(payload, &msg)
	return msg.Method
}

func readFrame(r *bufio.Reader) []byte {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil
	}
	n := int(hdr[1] & 0x7f)
	if n == 126 {
		var b [2]byte
		_, _ = io.ReadFull(r, b[:])
		n = int(b[0])<<8 | int(b[1])
	}
	var mask [4]byte
	_, _ = io.ReadFull(r, mask[:])
	p := make([]byte, n)
	_, _ = io.ReadFull(r, p)
	for i := range p {
		p[i] ^= mask[i%4]
	}
	return p
}
