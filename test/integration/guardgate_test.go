package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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
	buffer           []gatePacket
	bufferBytes      int
	session          string
	controlOnly      bool
	protectedOrigins []string
	sessions         map[string]string
	attaches         map[string]string
	urls             map[string][]string
	forwarded        map[string]bool
	ledger           []map[string]any
	auditFrom        int
	invalidFrames    int
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
	transform   func([]byte) []byte
	observe     func(gateCommand)
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
	c := &gateConnection{down: down, sessions: map[string]string{}, attaches: map[string]string{}, urls: map[string][]string{}, forwarded: map[string]bool{}}
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
			c.noteInbound(data, g.observe)
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
		if g.transform != nil {
			data = g.transform(data)
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

func gateTopKeyConflict(data []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return true
	}
	seen := map[string]bool{}
	folded := map[string]bool{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return true
		}
		key, ok := keyTok.(string)
		if !ok {
			return true
		}
		fold := strings.ToLower(key)
		if seen[key] || folded[fold] {
			return true
		}
		seen[key] = true
		folded[fold] = true
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return true
		}
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return true
	}
	_, err = dec.Token()
	return err != io.EOF
}

func gateNestedKeyConflict(raw json.RawMessage) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	return gateWalkDupKeys(dec)
}

func gateWalkDupKeys(dec *json.Decoder) bool {
	tok, err := dec.Token()
	if err != nil {
		return true
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return true
				}
				key, ok := keyTok.(string)
				if !ok || seen[key] {
					return true
				}
				seen[key] = true
				if gateWalkDupKeys(dec) {
					return true
				}
			}
			end, err := dec.Token()
			return err != nil || end != json.Delim('}')
		case '[':
			for dec.More() {
				if gateWalkDupKeys(dec) {
					return true
				}
			}
			end, err := dec.Token()
			return err != nil || end != json.Delim(']')
		default:
			return true
		}
	default:
		return false
	}
}

func gateParseCommand(data []byte) (gateCommand, bool) {
	if !json.Valid(data) || gateTopKeyConflict(data) {
		return gateCommand{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]json.RawMessage
	if dec.Decode(&raw) != nil {
		return gateCommand{}, false
	}
	for key := range raw {
		switch key {
		case "id", "method", "sessionId", "params":
		default:
			return gateCommand{}, false
		}
	}
	if idRaw, ok := raw["id"]; ok {
		var num json.Number
		if json.Unmarshal(idRaw, &num) != nil {
			return gateCommand{}, false
		}
		lit := num.String()
		if strings.ContainsAny(lit, ".eE") {
			return gateCommand{}, false
		}
		if _, err := strconv.ParseInt(lit, 10, 64); err != nil {
			return gateCommand{}, false
		}
	}
	var m gateCommand
	if json.Unmarshal(data, &m) != nil {
		return gateCommand{}, false
	}
	canon := map[string]any{}
	if _, ok := raw["id"]; ok {
		canon["id"] = m.ID
	}
	if _, ok := raw["method"]; ok {
		canon["method"] = m.Method
	}
	if m.Session != "" {
		canon["sessionId"] = m.Session
	} else if _, ok := raw["sessionId"]; ok {
		canon["sessionId"] = ""
	}
	if len(m.Params) > 0 && string(m.Params) != "null" {
		if gateNestedKeyConflict(m.Params) {
			return gateCommand{}, false
		}
		var params any
		if json.Unmarshal(m.Params, &params) != nil {
			return gateCommand{}, false
		}
		canon["params"] = params
	} else if _, ok := raw["params"]; ok && string(raw["params"]) != "null" {
		return gateCommand{}, false
	}
	encoded, err := json.Marshal(canon)
	if err != nil {
		return gateCommand{}, false
	}
	var original any
	var rebuilt any
	if json.Unmarshal(data, &original) != nil || json.Unmarshal(encoded, &rebuilt) != nil {
		return gateCommand{}, false
	}
	same, err := json.Marshal(original)
	if err != nil {
		return gateCommand{}, false
	}
	again, err := json.Marshal(rebuilt)
	if err != nil || !bytes.Equal(same, again) {
		return gateCommand{}, false
	}
	return m, true
}

func (c *gateConnection) noteInbound(data []byte, observe func(gateCommand)) {
	m, ok := gateParseCommand(data)
	if !ok {
		c.mu.Lock()
		c.invalidFrames++
		c.mu.Unlock()
		return
	}
	if observe != nil {
		observe(m)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forwarded[gateKey(m.Session, m.ID)] = true
	var params any
	_ = json.Unmarshal(m.Params, &params)
	entry := map[string]any{"method": m.Method, "id": m.ID}
	if m.Session != "" {
		entry["sessionId"] = m.Session
	}
	if params != nil {
		entry["params"] = params
	}
	c.ledger = append(c.ledger, entry)
	if m.Method == "Target.attachToTarget" {
		var attach struct {
			Target string `json:"targetId"`
		}
		_ = json.Unmarshal(m.Params, &attach)
		c.attaches[gateKey(m.Session, m.ID)] = attach.Target
	}
}

func ledgerID(entry map[string]any) (int64, bool) {
	switch v := entry["id"].(type) {
	case int64:
		return v, true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}

func (c *gateConnection) sessionCommandsLocked(session string) []map[string]any {
	start := c.auditFrom
	if start < 0 {
		start = 0
	}
	if start > len(c.ledger) {
		start = len(c.ledger)
	}
	commands := make([]map[string]any, 0)
	for _, e := range c.ledger[start:] {
		sid, _ := e["sessionId"].(string)
		if sid != session {
			continue
		}
		method, _ := e["method"].(string)
		if method == "" {
			continue
		}
		cmd := map[string]any{"method": method}
		if params, ok := e["params"]; ok && params != nil {
			cmd["params"] = params
		}
		commands = append(commands, cmd)
	}
	return commands
}

func (c *gateConnection) ledgerEntry(id int64, method, session string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	match := func(entry map[string]any, byID bool) bool {
		if byID {
			got, ok := ledgerID(entry)
			if !ok || got != id {
				return false
			}
		}
		if method != "" && entry["method"] != method {
			return false
		}
		if session != "" {
			s, _ := entry["sessionId"].(string)
			if s != session {
				return false
			}
		}
		return true
	}
	for i := len(c.ledger) - 1; i >= 0; i-- {
		if match(c.ledger[i], true) {
			return c.ledger[i]
		}
	}
	if method == "" {
		return nil
	}
	for i := len(c.ledger) - 1; i >= 0; i-- {
		if match(c.ledger[i], false) {
			return c.ledger[i]
		}
	}
	return nil
}

func (g *guardGate) ledgerEntry(id int64, method, session string) map[string]any {
	g.mu.Lock()
	conns := append([]*gateConnection(nil), g.connections...)
	g.mu.Unlock()
	for _, c := range conns {
		if entry := c.ledgerEntry(id, method, session); entry != nil {
			return entry
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
		Action    string   `json:"action"`
		Side      string   `json:"side"`
		Session   string   `json:"sessionId"`
		Target    string   `json:"targetId"`
		MS        int      `json:"ms"`
		WireID    int64    `json:"wireId"`
		Method    string   `json:"method"`
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
	if req.Action == "forwarded" {
		_ = json.NewEncoder(w).Encode(map[string]any{"entry": g.ledgerEntry(req.WireID, req.Method, req.Session)})
		return
	}
	c := g.connection(req.Session)
	if c == nil {
		http.Error(w, "session unavailable", http.StatusNotFound)
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
		c.first, c.released, c.events, c.invalidFrames = 0, 0, 0, 0
		c.auditFrom = len(c.ledger)
		result = map[string]any{"gate": true, "targetId": req.Target, "sessionId": req.Session, "ms": req.MS, "protectedObserved": protected, "ordinaryObserved": ordinary}
	case "audit":
		entries := make([]map[string]any, 0, len(req.Probes))
		for _, p := range req.Probes {
			entries = append(entries, map[string]any{"id": p.ID, "forwarded": c.forwarded[gateKey(req.Session, p.WireID)]})
		}
		result = map[string]any{"probes": entries, "commands": c.sessionCommandsLocked(req.Session), "invalidFrames": c.invalidFrames}
	default:
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}
