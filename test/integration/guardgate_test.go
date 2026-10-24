package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type gateCommand struct {
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Session string          `json:"sessionId"`
	Params  json.RawMessage `json:"params"`
}

type gatePacket struct {
	kind websocket.MessageType
	data []byte
}

type gateConnection struct {
	mu               sync.Mutex
	writeMu          sync.Mutex
	down             *websocket.Conn
	up               *websocket.Conn
	commandMethod    string
	commandSession   string
	commandPacket    *gatePacket
	buffer           []gatePacket
	bufferBytes      int
	session          string
	controlOnly      bool
	protectedOrigins []string
	sessions         map[string]string
	attaches         map[string]string
	urls             map[string][]string
	forwarded        map[string]bool
	hold             chan struct{}
	target           string
	minimum          time.Duration
	first            int64
	released         int64
	events           int
}

type guardGate struct {
	mu          sync.Mutex
	connections []*gateConnection
	srv         *httptest.Server
	endpoint    string
	origin      string
}

func gateKey(session string, id int64) string { return fmt.Sprintf("%s/%d", session, id) }

func newGuardGate(t *testing.T, endpoint, origin string) *guardGate {
	t.Helper()
	g := &guardGate{endpoint: endpoint, origin: origin}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", g.version)
	mux.HandleFunc("/json/list", g.list)
	mux.HandleFunc("/browser", g.socket)
	mux.HandleFunc("/gate", g.control)
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

func (g *guardGate) browserVersion(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.endpoint+"/json/version", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var v map[string]any
	err = json.NewDecoder(resp.Body).Decode(&v)
	return v, err
}

func (g *guardGate) version(w http.ResponseWriter, r *http.Request) {
	v, err := g.browserVersion(r.Context())
	if err != nil {
		http.Error(w, "browser unavailable", http.StatusBadGateway)
		return
	}
	v["webSocketDebuggerUrl"] = "ws://" + r.Host + "/browser"
	_ = json.NewEncoder(w).Encode(v)
}

func (g *guardGate) list(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, g.endpoint+"/json/list", nil)
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadGateway)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "browser unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	var v any
	if json.NewDecoder(resp.Body).Decode(&v) != nil {
		http.Error(w, "invalid list", http.StatusBadGateway)
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func (g *guardGate) socket(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	v, err := g.browserVersion(ctx)
	if err != nil {
		return
	}
	wsURL, _ := v["webSocketDebuggerUrl"].(string)
	up, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return
	}
	defer func() { _ = up.CloseNow() }()
	allowed, _ := url.Parse(g.origin)
	down, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{allowed.Host}})
	if err != nil {
		return
	}
	defer func() { _ = down.CloseNow() }()
	up.SetReadLimit(-1)
	down.SetReadLimit(-1)
	c := &gateConnection{down: down, up: up, sessions: map[string]string{}, attaches: map[string]string{}, urls: map[string][]string{}, forwarded: map[string]bool{}}
	g.mu.Lock()
	g.connections = append(g.connections, c)
	g.mu.Unlock()
	go func() {
		defer cancel()
		for {
			kind, data, err := down.Read(ctx)
			if err != nil {
				return
			}
			var m gateCommand
			if json.Unmarshal(data, &m) == nil {
				c.mu.Lock()
				c.forwarded[gateKey(m.Session, m.ID)] = true
				if m.Method == "Target.attachToTarget" {
					var params struct {
						Target string `json:"targetId"`
					}
					_ = json.Unmarshal(m.Params, &params)
					c.attaches[gateKey(m.Session, m.ID)] = params.Target
				}
				if c.commandMethod == m.Method && c.commandSession == m.Session && c.commandPacket == nil {
					c.commandPacket = &gatePacket{kind: kind, data: data}
					c.commandMethod = ""
					c.mu.Unlock()
					continue
				}
				c.mu.Unlock()
			}
			if up.Write(ctx, kind, data) != nil {
				return
			}
		}
	}()
	for {
		kind, data, err := up.Read(ctx)
		if err != nil {
			return
		}
		var m struct {
			Session string `json:"sessionId"`
			Method  string `json:"method"`
			Params  struct {
				TargetID   string `json:"targetId"`
				TargetInfo struct {
					ID  string `json:"targetId"`
					URL string `json:"url"`
				} `json:"targetInfo"`
				URL     string `json:"url"`
				FrameID string `json:"frameId"`
				Frame   struct {
					ID  string `json:"id"`
					URL string `json:"url"`
				} `json:"frame"`
			} `json:"params"`
		}
		_ = json.Unmarshal(data, &m)
		c.writeMu.Lock()
		c.mu.Lock()
		owned := m.Params.TargetID == c.target || m.Params.TargetInfo.ID == c.target || m.Params.FrameID == c.target || m.Params.Frame.ID == c.target
		held := c.hold != nil && (!c.controlOnly || m.Session == c.session || (m.Session == "" && owned))
		if held {
			c.buffer = append(c.buffer, gatePacket{kind: kind, data: data})
			c.bufferBytes += len(data)
			protected := false
			for _, raw := range []string{m.Params.URL, m.Params.TargetInfo.URL, m.Params.Frame.URL} {
				u, parseErr := url.Parse(raw)
				if parseErr != nil {
					continue
				}
				for _, origin := range c.protectedOrigins {
					protected = protected || u.Scheme+"://"+u.Host == origin
				}
			}
			if m.Method != "" && owned && protected {
				c.events++
				if c.first == 0 {
					c.first = time.Now().UnixMilli()
				}
			}
		}
		full := c.bufferBytes > 64<<20
		c.mu.Unlock()
		if !held {
			err = down.Write(ctx, kind, data)
			if err == nil {
				c.delivered(data)
			}
		}
		c.writeMu.Unlock()
		if err != nil || full {
			return
		}
	}
}

func (c *gateConnection) delivered(data []byte) {
	var m struct {
		ID      int64  `json:"id"`
		Session string `json:"sessionId"`
		Params  struct {
			Session    string `json:"sessionId"`
			TargetInfo struct {
				ID  string `json:"targetId"`
				URL string `json:"url"`
			} `json:"targetInfo"`
		} `json:"params"`
		Result struct {
			Session     string `json:"sessionId"`
			TargetInfos []struct {
				ID  string `json:"targetId"`
				URL string `json:"url"`
			} `json:"targetInfos"`
			TargetInfo struct {
				ID  string `json:"targetId"`
				URL string `json:"url"`
			} `json:"targetInfo"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if m.Result.Session != "" {
		if target := c.attaches[gateKey(m.Session, m.ID)]; target != "" {
			c.sessions[m.Result.Session] = target
		}
	}
	if m.Params.Session != "" && m.Params.TargetInfo.ID != "" {
		c.sessions[m.Params.Session] = m.Params.TargetInfo.ID
	}
	for _, info := range m.Result.TargetInfos {
		c.urls[info.ID] = append(c.urls[info.ID], info.URL)
	}
	if info := m.Result.TargetInfo; info.ID != "" {
		c.urls[info.ID] = append(c.urls[info.ID], info.URL)
	}
	if info := m.Params.TargetInfo; info.ID != "" {
		c.urls[info.ID] = append(c.urls[info.ID], info.URL)
	}
}

func (g *guardGate) connection(session string) *gateConnection {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.connections {
		c.mu.Lock()
		_, found := c.sessions[session]
		c.mu.Unlock()
		if found {
			return c
		}
	}
	return nil
}

func (g *guardGate) control(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != g.origin {
		http.Error(w, "wrong origin", http.StatusForbidden)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", g.origin)
	w.Header().Set("Access-Control-Allow-Headers", "content-type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req struct {
		Method    string   `json:"method"`
		Action    string   `json:"action"`
		Side      string   `json:"side"`
		Session   string   `json:"sessionId"`
		Target    string   `json:"targetId"`
		MS        int      `json:"ms"`
		Protected []string `json:"protectedOrigins"`
		Ordinary  string   `json:"ordinaryOrigin"`
		Probes    []struct {
			ID     int   `json:"id"`
			WireID int64 `json:"wireId"`
		} `json:"probes"`
	}
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "invalid control", http.StatusBadRequest)
		return
	}
	c := g.connection(req.Session)
	if c == nil {
		http.Error(w, "session unavailable", http.StatusNotFound)
		return
	}
	if req.Action == "release-command" {
		c.mu.Lock()
		packet := c.commandPacket
		c.commandPacket = nil
		c.mu.Unlock()
		if packet == nil || c.up.Write(r.Context(), packet.kind, packet.data) != nil {
			http.Error(w, "command unavailable", http.StatusConflict)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"released": true})
		return
	}
	if req.Action == "release" {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		c.mu.Lock()
		if c.hold != nil && c.first != 0 && time.Now().UnixMilli()-c.first < c.minimum.Milliseconds() {
			c.mu.Unlock()
			http.Error(w, "minimum hold not met", http.StatusConflict)
			return
		}
		packets := c.buffer
		c.buffer, c.bufferBytes = nil, 0
		if c.hold != nil {
			close(c.hold)
			c.hold = nil
			c.released = time.Now().UnixMilli()
		}
		result := map[string]any{"delayedEvents": c.events, "firstDelayedAt": c.first, "releasedAt": c.released}
		c.mu.Unlock()
		for _, p := range packets {
			if c.down.Write(r.Context(), p.kind, p.data) != nil {
				http.Error(w, "gate stream closed", http.StatusBadGateway)
				return
			}
			c.delivered(p.data)
		}
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var result any
	switch req.Action {
	case "arm-command":
		if req.Method == "" || c.commandMethod != "" || c.commandPacket != nil {
			http.Error(w, "command gate busy", http.StatusConflict)
			return
		}
		c.commandMethod, c.commandSession = req.Method, req.Session
		result = map[string]any{"armed": true}
	case "command-status":
		result = map[string]any{"queued": c.commandPacket != nil}
	case "arm":
		if c.hold != nil || c.sessions[req.Session] != req.Target {
			http.Error(w, "invalid gate", http.StatusConflict)
			return
		}
		protected, ordinary := false, false
		for _, raw := range c.urls[req.Target] {
			u, err := url.Parse(raw)
			if err != nil {
				continue
			}
			origin := u.Scheme + "://" + u.Host
			for _, p := range req.Protected {
				protected = protected || origin == p
			}
			ordinary = ordinary || (origin == req.Ordinary && strings.HasSuffix(u.Query().Get("n"), "-b"))
		}
		c.hold, c.target, c.minimum = make(chan struct{}), req.Target, time.Duration(req.MS)*time.Millisecond
		c.session, c.controlOnly = req.Session, req.Side == "control"
		c.protectedOrigins = append([]string(nil), req.Protected...)
		c.first, c.released, c.events = 0, 0, 0
		result = map[string]any{"gate": true, "targetId": req.Target, "sessionId": req.Session, "ms": req.MS, "protectedObserved": protected, "ordinaryObserved": ordinary}
	case "audit":
		entries := make([]map[string]any, 0, len(req.Probes))
		for _, p := range req.Probes {
			entries = append(entries, map[string]any{"id": p.ID, "forwarded": c.forwarded[gateKey(req.Session, p.WireID)]})
		}
		result = map[string]any{"probes": entries}
	default:
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}
