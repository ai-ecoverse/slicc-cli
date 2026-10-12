package cdp

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	cdpclient "github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/coder/websocket"
)

type Open struct {
	Kind    string `json:"kind"`
	Browser string `json:"browser"`
}

type Opened struct {
	Product         string `json:"product"`
	ProtocolVersion string `json:"protocolVersion"`
}

var dropped = [][]byte{
	[]byte(`{"method":"Network.webSocketFrameReceived"`),
	[]byte(`{"method":"Network.webSocketFrameSent"`),
}

var httpClient = cdpclient.EndpointClient(nil, 0)

func (r *Registry) Handler() link.Handler {
	return r.serve
}

func (r *Registry) serve(s *link.Stream) {
	var req Open
	if err := json.Unmarshal(s.OpenPayload(), &req); err != nil || req.Browser == "" {
		_ = s.Reset("EPROTO: cdp OPEN needs a browser")
		return
	}
	e := r.lookup(req.Browser)
	if e == nil {
		_ = s.Reset("ENOENT: no browser " + strconv.Quote(req.Browser))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), DialTimeout)
	go func() {
		select {
		case <-s.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	conn, opened, err := dial(ctx, e.ep)
	if err != nil {
		cancel()
		r.logf("cdp %s: %v", req.Browser, err)
		_ = s.Reset("ECONNREFUSED: " + err.Error())
		return
	}
	rl := &relay{s: s, conn: conn, logf: r.logf, id: req.Browser, g: newGuard(r.origins(), r.logf)}
	err = rl.bootstrap(ctx)
	cancel()
	if err != nil {
		_ = conn.CloseNow()
		r.logf("cdp %s: %v", req.Browser, err)
		_ = s.Reset("ECONNREFUSED: slicc can't watch the browser's targets: " + err.Error())
		return
	}
	if !r.track(e, rl) {
		_ = conn.CloseNow()
		_ = s.Reset("ENOENT: browser removed")
		return
	}
	defer r.untrack(e, rl)
	if err := s.Accept(opened); err != nil {
		_ = conn.CloseNow()
		return
	}
	rl.run()
}

type versionInfo struct {
	Browser         string `json:"Browser"`
	ProtocolVersion string `json:"Protocol-Version"`
	WebSocketURL    string `json:"webSocketDebuggerUrl"`
}

func dial(ctx context.Context, ep browser.Endpoint) (*websocket.Conn, Opened, error) {
	wsURL := ep.WebSocketURL
	var opened Opened
	if !ep.WSOnly {
		vi, err := fetchVersion(ctx, ep.CDP)
		if err != nil {
			return nil, opened, err
		}
		wsURL = vi.WebSocketURL
		opened = Opened{Product: vi.Browser, ProtocolVersion: vi.ProtocolVersion}
	}
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: httpClient, CompressionMode: websocket.CompressionDisabled})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, opened, fmt.Errorf("connect %s: %w", ep.CDP, err)
	}
	conn.SetReadLimit(MaxMessage)
	if ep.WSOnly {
		if opened, err = getVersion(ctx, conn); err != nil {
			_ = conn.CloseNow()
			return nil, opened, err
		}
	}
	return conn, opened, nil
}

func fetchVersion(ctx context.Context, base string) (versionInfo, error) {
	var vi versionInfo
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/json/version", nil)
	if err != nil {
		return vi, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return vi, fmt.Errorf("%s/json/version: %w", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return vi, fmt.Errorf("%s/json/version: %s", base, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&vi); err != nil {
		return vi, fmt.Errorf("%s/json/version: %w", base, err)
	}
	if vi.WebSocketURL == "" {
		return vi, fmt.Errorf("%s/json/version: no webSocketDebuggerUrl", base)
	}
	return vi, nil
}

const versionID = 1 << 30

func getVersion(ctx context.Context, conn *websocket.Conn) (Opened, error) {
	req, _ := json.Marshal(map[string]any{"id": versionID, "method": "Browser.getVersion"})
	if err := conn.Write(ctx, websocket.MessageText, req); err != nil {
		return Opened{}, fmt.Errorf("Browser.getVersion: %w", err)
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return Opened{}, fmt.Errorf("Browser.getVersion: %w", err)
		}
		var res struct {
			ID     int64  `json:"id"`
			Result Opened `json:"result"`
		}
		if json.Unmarshal(data, &res) == nil && res.ID == versionID {
			return res.Result, nil
		}
	}
}

type relay struct {
	s      *link.Stream
	conn   *websocket.Conn
	logf   func(format string, args ...any)
	id     string
	g      *guard
	pageMu sync.Mutex

	pageEnded   atomic.Bool
	browserDone atomic.Bool
	killOnce    sync.Once
}

func (rl *relay) kill(reason string) {
	rl.killOnce.Do(func() {
		_ = rl.s.Reset(reason)
		_ = rl.conn.CloseNow()
	})
}

func (rl *relay) run() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		rl.toBrowser()
	}()
	rl.toPage()
	<-done
}

func (rl *relay) toPage() {
	ctx := context.Background()
	for {
		typ, data, err := rl.conn.Read(ctx)
		if err != nil {
			rl.browserClosed(err)
			return
		}
		if typ != websocket.MessageText || filtered(data) {
			continue
		}
		v := rl.g.fromBrowser(data)
		if v.lost {
			time.AfterFunc(LostGrace, func() {
				if !rl.browserDone.Load() {
					rl.kill("ECONNRESET: " + refusedLost)
				}
			})
		}
		for _, msg := range v.send {
			_ = rl.conn.Write(ctx, websocket.MessageText, msg)
		}
		for _, id := range v.hold {
			rl.expireLater(id)
		}
		if v.forward != nil {
			v.page = append([][]byte{v.forward}, v.page...)
		}
		for _, msg := range v.page {
			if err := rl.writePage(msg); err != nil {
				_ = rl.conn.CloseNow()
				return
			}
		}
	}
}

func (rl *relay) expireLater(sessionID string) {
	time.AfterFunc(HoldTimeout, func() {
		for _, msg := range rl.g.expireHold(sessionID) {
			_ = rl.conn.Write(context.Background(), websocket.MessageText, msg)
		}
	})
}

func (rl *relay) writePage(data []byte) error {
	msg := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(msg, uint32(len(data)))
	copy(msg[4:], data)
	rl.pageMu.Lock()
	defer rl.pageMu.Unlock()
	_, err := rl.s.Write(msg)
	return err
}

func (rl *relay) browserClosed(err error) {
	rl.browserDone.Store(true)
	select {
	case <-rl.s.Done():
		return
	default:
	}
	switch {
	case rl.pageEnded.Load():
		_ = rl.s.CloseWrite()
	case errors.Is(err, websocket.ErrMessageTooBig):
		rl.kill("EMSGSIZE: a browser message over 64 MiB")
	case websocket.CloseStatus(err) == websocket.StatusNormalClosure:
		_ = rl.s.CloseWrite()
	default:
		rl.logf("cdp %s: browser socket: %v", rl.id, err)
		rl.kill("ECONNRESET: browser socket closed")
	}
}

func filtered(data []byte) bool {
	for _, p := range dropped {
		if bytes.HasPrefix(data, p) {
			return true
		}
	}
	return false
}

func (rl *relay) toBrowser() {
	ctx := context.Background()
	var hdr [4]byte
	var buf bytes.Buffer
	for {
		if _, err := io.ReadFull(rl.s, hdr[:]); err != nil {
			rl.pageClosed(err)
			return
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n > MaxMessage {
			rl.kill(fmt.Sprintf("EMSGSIZE: a message of %d bytes, over 64 MiB", n))
			return
		}
		buf.Reset()
		if _, err := io.CopyN(&buf, rl.s, int64(n)); err != nil {
			if errors.Is(err, io.EOF) {
				rl.kill("EPROTO: END inside a message")
				return
			}
			rl.pageClosed(err)
			return
		}
		if rl.browserDone.Load() {
			continue
		}
		out, ok := rl.checkPage(buf.Bytes())
		if !ok {
			return
		}
		if out == nil {
			continue
		}
		if err := rl.conn.Write(ctx, websocket.MessageText, out); err != nil {
			if rl.browserDone.Load() {
				continue
			}
			rl.logf("cdp %s: write: %v", rl.id, err)
			rl.kill("ECONNRESET: browser socket closed")
			return
		}
	}
}

func (rl *relay) pageClosed(err error) {
	if errors.Is(err, io.EOF) {
		rl.pageEnded.Store(true)
		if rl.browserDone.Load() {
			return
		}
		_ = rl.conn.Close(websocket.StatusNormalClosure, "")
		return
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		rl.kill("EPROTO: END inside a message")
		return
	}
	_ = rl.conn.CloseNow()
}
