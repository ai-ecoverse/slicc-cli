package ws

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/gorilla/websocket"
)

type session struct {
	s       *link.Stream
	c       *websocket.Conn
	mu      sync.Mutex
	own     []byte
	ownCode int
	timer   *time.Timer
	outcome string
}

func relay(ctx context.Context, s *link.Stream, c *websocket.Conn) string {
	x := &session{s: s, c: c}
	go func() {
		<-ctx.Done()
		_ = c.Close()
	}()
	upDone := make(chan struct{})
	go func() {
		defer close(upDone)
		x.up()
	}()
	x.down()
	select {
	case <-upDone:
	case <-s.Done():
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.timer != nil {
		x.timer.Stop()
	}
	return x.outcome
}

func (x *session) down() {
	var err error
	for err == nil {
		var t int
		var r io.Reader
		t, r, err = x.c.NextReader()
		if err == nil {
			err = x.forward(t, r)
		}
	}
	x.finish(err)
}

func (x *session) forward(t int, r io.Reader) error {
	rt := byte(RecordBinary)
	if t == websocket.TextMessage {
		rt = RecordText
	}
	var buf bytes.Buffer
	buf.Write([]byte{rt, 0, 0, 0, 0})
	if _, err := buf.ReadFrom(r); err != nil {
		return err
	}
	b := buf.Bytes()
	binary.BigEndian.PutUint32(b[1:], uint32(len(b)-RecordHeader))
	_, err := x.s.Write(b)
	return err
}

func (x *session) finish(err error) {
	rec, outcome := x.closing(err)
	if rec != nil {
		_, _ = x.s.Write(rec)
	}
	_ = x.s.CloseWrite()
	if errors.Is(err, websocket.ErrReadLimit) {
		_ = x.c.Close()
	}
	x.mu.Lock()
	x.outcome = outcome
	x.mu.Unlock()
}

func (x *session) closing(err error) ([]byte, string) {
	x.mu.Lock()
	own, code := x.own, x.ownCode
	x.mu.Unlock()
	var ce *websocket.CloseError
	switch {
	case own != nil:
		return own, "closed by the node with " + strconv.Itoa(code)
	case errors.As(err, &ce) && ce.Code != websocket.CloseAbnormalClosure:
		return CloseRecord(ce.Code, ce.Text), "closed with " + strconv.Itoa(ce.Code)
	case errors.Is(err, websocket.ErrReadLimit):
		return CloseRecord(CloseMessageTooBig, "a message over 64 MiB"), "closed by the node with 1009"
	}
	return nil, "dropped"
}

func (x *session) up() {
	defer func() { _, _ = io.Copy(io.Discard, x.s) }()
	head := make([]byte, RecordHeader)
	for {
		if _, err := io.ReadFull(x.s, head); err != nil {
			_ = x.c.Close()
			return
		}
		t, n := head[0], binary.BigEndian.Uint32(head[1:])
		switch {
		case t != RecordText && t != RecordBinary && t != RecordClose:
			x.fail(CloseProtocolError, "an unknown record type")
			return
		case n > MaxMessage:
			x.fail(CloseMessageTooBig, "a message over 64 MiB")
			return
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(x.s, payload); err != nil {
			_ = x.c.Close()
			return
		}
		if t == RecordClose {
			code, reason, ok := ParseClose(payload)
			if !ok {
				x.fail(CloseProtocolError, "a malformed close record")
				return
			}
			x.sendClose(code, reason)
			return
		}
		mt := websocket.BinaryMessage
		if t == RecordText {
			mt = websocket.TextMessage
		}
		if err := x.c.WriteMessage(mt, payload); err != nil {
			_ = x.c.Close()
			return
		}
	}
}

func (x *session) fail(code int, reason string) {
	x.mu.Lock()
	if x.own == nil {
		x.own, x.ownCode = CloseRecord(code, reason), code
	}
	x.mu.Unlock()
	x.sendClose(code, reason)
}

func (x *session) sendClose(code int, reason string) {
	err := x.c.WriteControl(websocket.CloseMessage, ClosePayload(code, reason), time.Now().Add(CloseTimeout))
	if err != nil && !errors.Is(err, websocket.ErrCloseSent) {
		_ = x.c.Close()
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.timer == nil {
		x.timer = time.AfterFunc(CloseTimeout, func() { _ = x.c.Close() })
	}
}
