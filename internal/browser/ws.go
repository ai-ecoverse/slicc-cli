package browser

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

func cdpBrowserCall(ctx context.Context, wsURL, method string, timeout time.Duration) error {
	u, err := url.Parse(wsURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := dialWS(ctx, u)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	br := bufio.NewReader(conn)
	if err := wsHandshake(conn, br, u); err != nil {
		return err
	}
	msg, _ := json.Marshal(map[string]any{"id": 1, "method": method})
	if err := writeWSFrame(conn, 0x1, msg); err != nil {
		return err
	}
	_, err = readWSFrame(br)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	return err
}

func dialWS(ctx context.Context, u *url.URL) (net.Conn, error) {
	d := &net.Dialer{}
	switch u.Scheme {
	case "ws":
		return d.DialContext(ctx, "tcp", u.Host)
	case "wss":
		td := &tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}}
		return td.DialContext(ctx, "tcp", u.Host)
	}
	return nil, fmt.Errorf("unsupported websocket scheme %q", u.Scheme)
}

func wsHandshake(conn net.Conn, br *bufio.Reader, u *url.URL) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	path := u.RequestURI()
	req := "GET " + path + " HTTP/1.1\r\nHost: " + u.Host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(nonce[:]) + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		return err
	}
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("websocket handshake: %s", resp.Status)
	}
	return nil
}

func writeWSFrame(w io.Writer, opcode byte, payload []byte) error {
	hdr := []byte{0x80 | opcode}
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n <= 0xffff:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 0x80|127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	hdr = append(hdr, mask[:]...)
	masked := make([]byte, n)
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	_, err := w.Write(append(hdr, masked...))
	return err
}

func readWSFrame(r *bufio.Reader) ([]byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > 1<<20 {
		return nil, errors.New("websocket frame too large")
	}
	var mask [4]byte
	if h[1]&0x80 != 0 {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	if h[1]&0x80 != 0 {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return payload, nil
}
