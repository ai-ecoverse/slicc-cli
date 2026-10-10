package integration

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	bin "encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type cdpConn struct {
	conn net.Conn
	br   *bufio.Reader
	id   int
}

func dialCDP(wsURL string) (*cdpConn, error) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	req := "GET " + u.RequestURI() + " HTTP/1.1\r\nHost: " + u.Host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(nonce[:]) + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket handshake: %v %v", resp, err)
	}
	return &cdpConn{conn: conn, br: br}, nil
}

func (c *cdpConn) close() { _ = c.conn.Close() }

func (c *cdpConn) send(payload []byte) error {
	hdr := []byte{0x81}
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	default:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	}
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	hdr = append(hdr, mask[:]...)
	for i, b := range payload {
		hdr = append(hdr, b^mask[i%4])
	}
	_, err := c.conn.Write(hdr)
	return err
}

func (c *cdpConn) frame() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(c.br, h[:]); err != nil {
			return nil, err
		}
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(c.br, b[:]); err != nil {
				return nil, err
			}
			n = uint64(bin.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(c.br, b[:]); err != nil {
				return nil, err
			}
			n = bin.BigEndian.Uint64(b[:])
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(c.br, p); err != nil {
			return nil, err
		}
		msg = append(msg, p...)
		if h[0]&0x80 != 0 {
			return msg, nil
		}
	}
}

func (c *cdpConn) call(method string, params any) (json.RawMessage, error) {
	c.id++
	b, _ := json.Marshal(map[string]any{"id": c.id, "method": method, "params": params})
	if err := c.send(b); err != nil {
		return nil, err
	}
	for {
		f, err := c.frame()
		if err != nil {
			return nil, err
		}
		var resp struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(f, &resp) != nil || resp.ID != c.id {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func chromeCommandLine(t *testing.T, base string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, base+"/json/new?chrome://version", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var target struct {
		WebSocketURL string `json:"webSocketDebuggerUrl"`
	}
	err = json.NewDecoder(resp.Body).Decode(&target)
	_ = resp.Body.Close()
	if err != nil || target.WebSocketURL == "" {
		t.Fatalf("/json/new: %v %+v", err, target)
	}
	c, err := dialCDP(target.WebSocketURL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		res, err := c.call("Runtime.evaluate", map[string]any{
			"expression":    `(document.getElementById("command_line") || {}).innerText || ""`,
			"returnByValue": true,
		})
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		if json.Unmarshal(res, &v) == nil && strings.TrimSpace(v.Result.Value) != "" {
			return v.Result.Value
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("chrome://version showed no command line")
	return ""
}
