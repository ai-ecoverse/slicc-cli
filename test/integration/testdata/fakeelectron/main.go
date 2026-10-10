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
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	port := ""
	for _, a := range os.Args[1:] {
		if v, ok := strings.CutPrefix(a, "--remote-debugging-port="); ok {
			port = v
		}
	}
	stubborn := os.Getenv("FAKE_ELECTRON_MODE") == "stubborn"
	if stubborn {
		signal.Ignore(syscall.SIGTERM, os.Interrupt)
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/150.0.0.0",
			"User-Agent":           "Mozilla/5.0 Fake/1.0 Chrome/150.0.0.0 Electron/38.0.0 Safari/537.36",
			"webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/fake",
		})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "[]")
	})
	mux.HandleFunc("/devtools/browser/fake", func(w http.ResponseWriter, r *http.Request) {
		method := acceptOne(w, r)
		if method == "Browser.close" && !stubborn {
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
