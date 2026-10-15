package link

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type ResetError struct {
	Code    string
	Message string
	Remote  bool
}

func (e *ResetError) Error() string {
	side := "local"
	if e.Remote {
		side = "remote"
	}
	if e.Message != "" {
		return "stream reset (" + side + "): " + e.Code + ": " + e.Message
	}
	return "stream reset (" + side + "): " + e.Code
}

func (e *ResetError) Reason() string {
	if e.Message != "" {
		return e.Code + ": " + e.Message
	}
	return e.Code
}

func ParseReset(reason string) *ResetError {
	code, msg, _ := strings.Cut(reason, ":")
	return &ResetError{Code: strings.TrimSpace(code), Message: strings.TrimSpace(msg)}
}

var (
	ErrNotAccepted   = errors.New("link: stream not accepted yet")
	ErrAlreadyOpened = errors.New("link: stream already answered")
	ErrWriteAfterEnd = errors.New("link: write after END")
)

type Stream struct {
	l     *Link
	ch    *channel
	id    uint32
	kind  string
	open  json.RawMessage
	local bool

	mu          sync.Mutex
	cond        *sync.Cond
	opened      json.RawMessage
	estab       bool
	answered    chan struct{}
	rbuf        [][]byte
	rlen        int
	peerEnd     bool
	sentEnd     bool
	outstanding int
	reset       *ResetError
	finished    bool
	done        chan struct{}
	readBy      time.Time
	writeBy     time.Time
	readTimer   *time.Timer
	writeTimer  *time.Timer
}

func newStream(l *Link, ch *channel, id uint32, kind string, open json.RawMessage, local bool) *Stream {
	s := &Stream{l: l, ch: ch, id: id, kind: kind, open: open, local: local, answered: make(chan struct{}), done: make(chan struct{})}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *Stream) ID() uint32 { return s.id }

func (s *Stream) Kind() string { return s.kind }

func (s *Stream) Local() bool { return s.local }

func (s *Stream) Shared() bool { return s.ch.control }

func (s *Stream) Link() *Link { return s.l }

func (s *Stream) OpenPayload() json.RawMessage { return s.open }

func (s *Stream) OpenedPayload() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened
}

func (s *Stream) Done() <-chan struct{} { return s.done }

func (s *Stream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reset != nil {
		return s.reset
	}
	return nil
}

func (s *Stream) send(t FrameType, payload []byte) error {
	return s.ch.send(EncodeFrame(t, s.id, payload))
}

func (s *Stream) Accept(opened any) error {
	payload := []byte("{}")
	if opened != nil {
		b, err := json.Marshal(opened)
		if err != nil {
			return err
		}
		payload = b
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reset != nil {
		return s.reset
	}
	if s.local || s.estab {
		return ErrAlreadyOpened
	}
	if err := s.send(FrameOpened, payload); err != nil {
		return err
	}
	s.opened = payload
	s.estab = true
	close(s.answered)
	return nil
}

func (s *Stream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.rbuf) == 0 && !s.peerEnd && s.reset == nil {
		if expired(s.readBy) {
			return 0, os.ErrDeadlineExceeded
		}
		s.cond.Wait()
	}
	if s.reset != nil {
		return 0, s.reset
	}
	if len(s.rbuf) == 0 {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && len(s.rbuf) > 0 {
		c := copy(p[n:], s.rbuf[0])
		n += c
		if c == len(s.rbuf[0]) {
			s.rbuf = s.rbuf[1:]
		} else {
			s.rbuf[0] = s.rbuf[0][c:]
		}
	}
	s.creditLocked(n)
	return n, nil
}

func (s *Stream) WriteTo(w io.Writer) (int64, error) {
	var total int64
	for {
		s.mu.Lock()
		for len(s.rbuf) == 0 && !s.peerEnd && s.reset == nil {
			s.cond.Wait()
		}
		if s.reset != nil {
			err := s.reset
			s.mu.Unlock()
			return total, err
		}
		if len(s.rbuf) == 0 {
			s.mu.Unlock()
			return total, nil
		}
		chunk := s.rbuf[0]
		s.rbuf = s.rbuf[1:]
		s.mu.Unlock()
		n, err := w.Write(chunk)
		total += int64(n)
		s.mu.Lock()
		s.creditLocked(len(chunk))
		s.mu.Unlock()
		if err != nil {
			return total, err
		}
	}
}

func (s *Stream) creditLocked(n int) {
	if n <= 0 {
		return
	}
	s.rlen -= n
	if s.finished || s.reset != nil || s.peerEnd {
		return
	}
	_ = s.send(FrameCredit, creditPayload(n))
}

func (s *Stream) Write(p []byte) (int, error) {
	total := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(p) > 0 {
		for s.reset == nil && !s.sentEnd && s.estab && s.outstanding >= Window {
			if expired(s.writeBy) {
				return total, os.ErrDeadlineExceeded
			}
			s.cond.Wait()
		}
		switch {
		case s.reset != nil:
			return total, s.reset
		case s.sentEnd:
			return total, ErrWriteAfterEnd
		case !s.estab:
			return total, ErrNotAccepted
		}
		n := min(len(p), MaxPayload, Window-s.outstanding)
		if err := s.send(FrameData, p[:n]); err != nil {
			s.resetLocked(&ResetError{Code: "ECONNRESET", Message: err.Error()}, false)
			return total, s.reset
		}
		s.outstanding += n
		p = p[n:]
		total += n
	}
	return total, nil
}

func (s *Stream) TryWrite(p []byte) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.reset != nil:
		return false, s.reset
	case s.sentEnd:
		return false, ErrWriteAfterEnd
	case !s.estab:
		return false, ErrNotAccepted
	case len(p) > Window-s.outstanding:
		return false, nil
	}
	for len(p) > 0 {
		n := min(len(p), MaxPayload)
		if err := s.send(FrameData, p[:n]); err != nil {
			s.resetLocked(&ResetError{Code: "ECONNRESET", Message: err.Error()}, false)
			return false, s.reset
		}
		s.outstanding += n
		p = p[n:]
	}
	return true, nil
}

func (s *Stream) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readBy = t
	s.readTimer = s.armLocked(s.readTimer, t)
	return nil
}

func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeBy = t
	s.writeTimer = s.armLocked(s.writeTimer, t)
	return nil
}

func (s *Stream) armLocked(old *time.Timer, t time.Time) *time.Timer {
	if old != nil {
		old.Stop()
	}
	s.cond.Broadcast()
	if t.IsZero() {
		return nil
	}
	return time.AfterFunc(time.Until(t), func() {
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	})
}

func expired(t time.Time) bool {
	return !t.IsZero() && !time.Now().Before(t)
}

func (s *Stream) CloseWrite() error {
	s.mu.Lock()
	if s.reset != nil {
		err := s.reset
		s.mu.Unlock()
		return err
	}
	if s.sentEnd {
		s.mu.Unlock()
		return nil
	}
	if !s.estab {
		s.mu.Unlock()
		return ErrNotAccepted
	}
	err := s.send(FrameEnd, nil)
	s.sentEnd = true
	fin := s.peerEnd && s.finishLocked()
	s.mu.Unlock()
	if fin {
		s.l.release(s)
	}
	return err
}

func (s *Stream) Reset(reason string) error {
	if reason == "" {
		reason = "ECONNRESET"
	}
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return nil
	}
	err := s.send(FrameReset, []byte(reason))
	e := ParseReset(reason)
	fin := s.resetLocked(e, true)
	s.mu.Unlock()
	if fin {
		s.l.release(s)
	}
	return err
}

func (s *Stream) resetLocked(e *ResetError, sent bool) bool {
	if s.finished {
		return false
	}
	if !sent && !e.Remote {
		_ = s.send(FrameReset, []byte(e.Reason()))
	}
	s.reset = e
	s.rbuf = nil
	return s.finishLocked()
}

func (s *Stream) finishLocked() bool {
	if s.finished {
		return false
	}
	s.finished = true
	select {
	case <-s.answered:
	default:
		close(s.answered)
	}
	close(s.done)
	s.cond.Broadcast()
	return true
}

func (s *Stream) onFrame(f Frame) (violation string, fin bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return "", false
	}
	switch f.Type {
	case FrameOpened:
		if !s.local || s.estab {
			return "unexpected OPENED", false
		}
		s.opened = append(json.RawMessage(nil), f.Payload...)
		s.estab = true
		close(s.answered)
	case FrameData:
		switch {
		case !s.estab:
			return "DATA before OPENED", false
		case s.peerEnd:
			return "DATA after END", false
		case len(f.Payload) == 0:
			return "empty DATA", false
		case s.rlen+len(f.Payload) > Window:
			return "DATA past the window", false
		}
		s.rlen += len(f.Payload)
		s.rbuf = append(s.rbuf, append([]byte(nil), f.Payload...))
		s.cond.Broadcast()
	case FrameEnd:
		switch {
		case !s.estab:
			return "END before OPENED", false
		case s.peerEnd:
			return "END after END", false
		}
		s.peerEnd = true
		s.cond.Broadcast()
		if s.sentEnd {
			return "", s.finishLocked()
		}
	case FrameReset:
		e := ParseReset(string(f.Payload))
		e.Remote = true
		return "", s.resetLocked(e, true)
	case FrameCredit:
		n := int(uint32(f.Payload[0])<<24 | uint32(f.Payload[1])<<16 | uint32(f.Payload[2])<<8 | uint32(f.Payload[3]))
		if n < 1 || n > s.outstanding {
			return "CREDIT past what was sent", false
		}
		s.outstanding -= n
		s.cond.Broadcast()
	}
	return "", false
}

func (s *Stream) abort(e *ResetError) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return false
	}
	s.reset = e
	s.rbuf = nil
	return s.finishLocked()
}
