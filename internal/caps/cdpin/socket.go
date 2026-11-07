package cdpin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/coder/websocket"
)

const (
	lostLink     = "lost the link to seven"
	attachWithin = 30 * time.Second
	endWithin    = 5 * time.Second
	linkGrace    = 3 * time.Second
)

type socket struct {
	s      *Server
	conn   *websocket.Conn
	st     *link.Stream
	target string
	sid    string
	once   sync.Once
	done   chan struct{}
}

func (s *Server) serveSocket(w http.ResponseWriter, r *http.Request, target string) {
	st, err := s.openStream(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		_ = st.Reset("ECONNRESET: the client went away")
		return
	}
	conn.SetReadLimit(MaxMessage)
	k := &socket{s: s, conn: conn, st: st, target: target, done: make(chan struct{})}
	if !s.track(k) {
		k.shut(1001, "slicc is stopping")
		return
	}
	defer s.untrack(k)
	kind := "browser"
	if target != "" {
		kind = "page"
	}
	s.cfg.Logf("cdp: %s socket open", kind)
	if target != "" && !k.attach() {
		return
	}
	go k.toClient()
	k.toBrowser()
	<-k.done
	s.cfg.Logf("cdp: %s socket closed", kind)
}

func (k *socket) shut(code websocket.StatusCode, reason string) {
	k.once.Do(func() {
		if code == websocket.StatusNormalClosure || code == websocket.StatusGoingAway || code == websocket.StatusUnsupportedData {
			finish(k.st)
		}
		_ = k.conn.Close(code, trimReason(reason))
		_ = k.conn.CloseNow()
	})
}

func trimReason(s string) string {
	if len(s) <= 123 {
		return s
	}
	b := []byte(s[:120])
	for len(b) > 0 && b[len(b)-1]&0xc0 == 0x80 {
		b = b[:len(b)-1]
	}
	if len(b) > 0 && b[len(b)-1] >= 0xc0 {
		b = b[:len(b)-1]
	}
	return string(b) + "..."
}

func (k *socket) attach() bool {
	req, _ := json.Marshal(map[string]any{"id": attachID, "method": "Target.attachToTarget", "params": map[string]any{"targetId": k.target, "flatten": true}})
	if err := writeFrame(k.st, req); err != nil {
		k.streamEnded(err)
		return false
	}
	_ = k.st.SetReadDeadline(time.Now().Add(attachWithin))
	defer func() { _ = k.st.SetReadDeadline(time.Time{}) }()
	for {
		msg, err := readFrame(k.st)
		if err != nil {
			if errors.Is(err, io.EOF) {
				k.shut(websocket.StatusInternalError, "seven closed the connection")
				return false
			}
			k.streamEnded(err)
			return false
		}
		var a struct {
			ID     *int64 `json:"id"`
			Result struct {
				SessionID string `json:"sessionId"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(msg, &a) != nil || a.ID == nil || *a.ID != attachID {
			continue
		}
		switch {
		case a.Error != nil:
			k.shut(websocket.StatusInternalError, a.Error.Message)
		case a.Result.SessionID == "":
			k.shut(websocket.StatusInternalError, "no session for that target")
		default:
			k.sid = a.Result.SessionID
			return true
		}
		_ = k.st.Reset("ECONNRESET: the client went away")
		return false
	}
}

func (k *socket) toClient() {
	defer close(k.done)
	ctx := k.s.ctx
	for {
		msg, err := readFrame(k.st)
		if err != nil {
			k.streamEnded(err)
			return
		}
		out, end := k.fromBrowser(msg)
		if end {
			k.shut(websocket.StatusNormalClosure, "")
			return
		}
		if out == nil {
			continue
		}
		if err := k.conn.Write(ctx, websocket.MessageText, out); err != nil {
			_ = k.st.Reset("ECONNRESET: the client went away")
			k.shut(websocket.StatusGoingAway, "")
			return
		}
	}
}

func (k *socket) streamEnded(err error) {
	var re *link.ResetError
	switch {
	case errors.Is(err, io.EOF):
		k.shut(websocket.StatusNormalClosure, "")
	case errors.Is(err, errMidMessage):
		_ = k.st.Reset(errMidMessage.Error())
		k.shut(websocket.StatusInternalError, errMidMessage.Error())
	case errors.Is(err, errTooBig):
		_ = k.st.Reset(errTooBig.Error())
		k.shut(websocket.StatusMessageTooBig, errTooBig.Error())
	case k.linkLost(err):
		k.shut(websocket.StatusInternalError, lostLink)
	case errors.As(err, &re) && re.Remote && re.Code == "EACCES":
		k.shut(websocket.StatusPolicyViolation, re.Message)
	case errors.As(err, &re) && re.Remote:
		k.shut(websocket.StatusInternalError, re.Reason())
	default:
		k.shut(websocket.StatusInternalError, "")
	}
}

func (k *socket) linkLost(err error) bool {
	var re *link.ResetError
	if errors.As(err, &re) && !re.Remote {
		select {
		case <-k.st.Link().Done():
		case <-time.After(linkGrace):
		}
	}
	return closed(k.st.Link().Done())
}

func (k *socket) toBrowser() {
	ctx := k.s.ctx
	for {
		typ, data, err := k.conn.Read(ctx)
		if err != nil {
			k.clientEnded(err)
			return
		}
		if typ != websocket.MessageText {
			k.shut(websocket.StatusUnsupportedData, "binary messages aren't CDP")
			return
		}
		if k.sid != "" {
			data = withSession(data, k.sid)
			if len(data) > MaxMessage {
				_ = k.st.Reset("EMSGSIZE: a client message over 64 MiB with its session")
				k.shut(websocket.StatusMessageTooBig, "a message over 64 MiB")
				return
			}
		}
		if err := writeFrame(k.st, data); err != nil {
			return
		}
	}
}

func (k *socket) clientEnded(err error) {
	if errors.Is(err, websocket.ErrMessageTooBig) {
		_ = k.st.Reset("EMSGSIZE: a client message over 64 MiB")
		k.shut(websocket.StatusMessageTooBig, "a message over 64 MiB")
		return
	}
	select {
	case <-k.done:
		return
	default:
	}
	_ = k.st.CloseWrite()
	select {
	case <-k.done:
	case <-time.After(endWithin):
		_ = k.st.Reset("ECONNRESET: the client went away")
	}
	k.once.Do(func() { _ = k.conn.CloseNow() })
}

func withSession(data []byte, sid string) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil || m == nil {
		return data
	}
	if _, ok := m["sessionId"]; ok {
		return data
	}
	m["sessionId"], _ = json.Marshal(sid)
	out, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return out
}

type browserMessage struct {
	ID        *int64 `json:"id"`
	Method    string `json:"method"`
	SessionID string `json:"sessionId"`
	Params    struct {
		SessionID string `json:"sessionId"`
		TargetID  string `json:"targetId"`
	} `json:"params"`
}

func (k *socket) fromBrowser(msg []byte) ([]byte, bool) {
	if k.sid == "" {
		return msg, false
	}
	var m browserMessage
	if json.Unmarshal(msg, &m) != nil {
		return nil, false
	}
	switch {
	case m.SessionID == k.sid:
		return withoutSession(msg), false
	case m.SessionID != "":
		return msg, false
	case m.Method == "Target.detachedFromTarget" && m.Params.SessionID == k.sid:
		return nil, true
	case m.Method == "Target.targetDestroyed" && m.Params.TargetID == k.target:
		return nil, true
	}
	return nil, false
}

func withoutSession(data []byte) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return data
	}
	delete(m, "sessionId")
	out, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return out
}
