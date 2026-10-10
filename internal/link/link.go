package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

type Handler func(*Stream)

type Options struct {
	Hello        Hello
	Handlers     map[string]Handler
	Logf         func(format string, args ...any)
	HelloTimeout time.Duration
	MaxChannels  int
	Certificates []webrtc.Certificate
}

type OpenOptions struct {
	Shared bool
}

var (
	ErrClosed       = errors.New("link: closed")
	ErrHelloTimeout = errors.New("link: no HELLO from the peer")
)

type Link struct {
	pc   *webrtc.PeerConnection
	role Role
	opts Options

	mu        sync.Mutex
	control   *channel
	streams   map[uint32]*Stream
	peerIDs   idSet
	nextID    uint32
	ownChans  int
	peerHello *Hello
	waiting   []*channel
	helloCh   chan struct{}
	err       error
	done      chan struct{}
}

type channel struct {
	l       *Link
	dc      *webrtc.DataChannel
	control bool
	own     bool

	proc    sync.Mutex
	pending [][]byte

	mu       sync.Mutex
	used     bool
	stream   *Stream
	released bool
	sendOnce sync.Once
}

func New(pc *webrtc.PeerConnection, role Role, opts Options) (*Link, error) {
	if opts.HelloTimeout <= 0 {
		opts.HelloTimeout = 10 * time.Second
	}
	if opts.MaxChannels <= 0 {
		opts.MaxChannels = 256
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	opts.Hello.V = Version
	opts.Hello.Role = role
	if opts.Hello.Caps == nil {
		opts.Hello.Caps = []string{}
	}
	l := &Link{
		pc:      pc,
		role:    role,
		opts:    opts,
		streams: map[uint32]*Stream{},
		helloCh: make(chan struct{}),
		done:    make(chan struct{}),
		nextID:  2,
	}
	if role == RolePage {
		l.nextID = 1
	}
	l.peerIDs.floor = 1
	if role.peer() == RoleNode {
		l.peerIDs.floor = 2
	}
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		switch st {
		case webrtc.PeerConnectionStateFailed:
			l.fail(errors.New("link: connection failed"))
		case webrtc.PeerConnectionStateClosed:
			l.fail(ErrClosed)
		}
	})
	pc.OnDataChannel(l.onDataChannel)
	if role == RolePage {
		dc, err := pc.CreateDataChannel(ControlName, nil)
		if err != nil {
			return nil, err
		}
		l.setControl(dc)
	}
	return l, nil
}

func (l *Link) PeerConnection() *webrtc.PeerConnection { return l.pc }

func (l *Link) Role() Role { return l.role }

func (l *Link) Done() <-chan struct{} { return l.done }

func (l *Link) Ready() <-chan struct{} { return l.helloCh }

func (l *Link) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func (l *Link) PeerHello() (Hello, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.peerHello == nil {
		return Hello{}, false
	}
	return *l.peerHello, true
}

func (l *Link) Close() error {
	l.fail(ErrClosed)
	return l.pc.Close()
}

func (l *Link) fail(err error) {
	l.mu.Lock()
	if l.err != nil {
		l.mu.Unlock()
		return
	}
	l.err = err
	streams := make([]*Stream, 0, len(l.streams))
	for _, s := range l.streams {
		streams = append(streams, s)
	}
	l.streams = map[uint32]*Stream{}
	close(l.done)
	l.mu.Unlock()
	l.opts.Logf("link: closing: %v", err)
	for _, s := range streams {
		s.abort(&ResetError{Code: "ECONNRESET", Message: "link closed"})
	}
	go func() { _ = l.pc.Close() }()
}

func (l *Link) violation(format string, args ...any) {
	l.fail(fmt.Errorf("link: protocol violation: "+format, args...))
}

func (l *Link) onDataChannel(dc *webrtc.DataChannel) {
	switch dc.Label() {
	case ControlName:
		l.mu.Lock()
		dup := l.control != nil
		l.mu.Unlock()
		if dup {
			l.violation("a second control channel")
			return
		}
		l.setControl(dc)
	case StreamName:
		l.wireChannel(&channel{l: l, dc: dc})
	default:
		l.opts.Logf("link: closing data channel with unknown label %q", dc.Label())
		_ = dc.Close()
	}
}

func (l *Link) setControl(dc *webrtc.DataChannel) {
	ch := &channel{l: l, dc: dc, control: true}
	l.mu.Lock()
	l.control = ch
	l.mu.Unlock()
	gotHello := false
	dc.OnOpen(func() { ch.sendOnce.Do(func() { l.sendHello(ch) }) })
	dc.OnClose(func() { l.fail(errors.New("link: control channel closed")) })
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if !gotHello {
			if !m.IsString {
				l.violation("first control message is not HELLO")
				return
			}
			gotHello = true
			l.onHello(m.Data)
			return
		}
		if m.IsString {
			l.violation("text message after HELLO")
			return
		}
		l.processFrame(ch, m.Data)
	})
	if dc.ReadyState() == webrtc.DataChannelStateOpen {
		ch.sendOnce.Do(func() { l.sendHello(ch) })
	}
}

func (l *Link) sendHello(ch *channel) {
	b, err := json.Marshal(l.opts.Hello)
	if err != nil {
		l.fail(err)
		return
	}
	if err := ch.dc.SendText(string(b)); err != nil {
		l.fail(fmt.Errorf("link: send HELLO: %w", err))
		return
	}
	time.AfterFunc(l.opts.HelloTimeout, func() {
		select {
		case <-l.helloCh:
		default:
			l.fail(ErrHelloTimeout)
		}
	})
}

func (l *Link) onHello(data []byte) {
	h, err := parseHello(data, l.role.peer())
	if err != nil {
		l.fail(fmt.Errorf("link: %w", err))
		return
	}
	l.mu.Lock()
	l.peerHello = &h
	waiting := l.waiting
	l.waiting = nil
	close(l.helloCh)
	l.mu.Unlock()
	l.opts.Logf("link: HELLO from %s %q", h.Role, h.Name)
	for _, ch := range waiting {
		ch.drain()
	}
}

func (l *Link) helloDone() bool {
	select {
	case <-l.helloCh:
		return true
	default:
		return false
	}
}

func (l *Link) wireChannel(ch *channel) {
	ch.dc.OnClose(func() { ch.onClose() })
	ch.dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if m.IsString {
			l.violation("text message on a stream channel")
			return
		}
		ch.proc.Lock()
		defer ch.proc.Unlock()
		if !l.helloDone() {
			l.mu.Lock()
			if l.peerHello == nil {
				if len(ch.pending) == 0 {
					l.waiting = append(l.waiting, ch)
				}
				ch.pending = append(ch.pending, m.Data)
				l.mu.Unlock()
				return
			}
			l.mu.Unlock()
		}
		ch.drainLocked()
		l.processFrame(ch, m.Data)
	})
}

func (ch *channel) drain() {
	ch.proc.Lock()
	defer ch.proc.Unlock()
	ch.drainLocked()
}

func (ch *channel) drainLocked() {
	pending := ch.pending
	ch.pending = nil
	for _, p := range pending {
		ch.l.processFrame(ch, p)
	}
}

func (ch *channel) send(b []byte) error {
	return ch.dc.Send(b)
}

func (ch *channel) onClose() {
	ch.mu.Lock()
	s := ch.stream
	ch.mu.Unlock()
	if s != nil && s.abort(&ResetError{Code: "ECONNRESET", Message: "stream channel closed"}) {
		ch.l.release(s)
	}
}

func (l *Link) processFrame(ch *channel, data []byte) {
	f, err := DecodeFrame(data)
	if err != nil {
		l.violation("%v", err)
		return
	}
	if f.Type == FrameOpen {
		l.handleOpen(ch, f)
		return
	}
	l.mu.Lock()
	s := l.streams[f.ID]
	l.mu.Unlock()
	if s == nil || s.ch != ch {
		return
	}
	bad, fin := s.onFrame(f)
	if bad != "" {
		l.opts.Logf("link: stream %d: %s", s.id, bad)
		_ = s.Reset("EPROTO: " + bad)
		return
	}
	if fin {
		l.release(s)
	}
}

func (l *Link) handleOpen(ch *channel, f Frame) {
	peerOdd := l.role.peer() == RolePage
	if (f.ID%2 == 1) != peerOdd {
		l.violation("OPEN %d has the wrong parity", f.ID)
		return
	}
	var head struct {
		Kind string `json:"kind"`
	}
	headErr := json.Unmarshal(f.Payload, &head)
	l.mu.Lock()
	if l.err != nil {
		l.mu.Unlock()
		return
	}
	if !l.peerIDs.add(f.ID) {
		l.mu.Unlock()
		l.violation("duplicate stream id %d", f.ID)
		return
	}
	if !ch.control && !ch.claim() {
		l.mu.Unlock()
		l.violation("a second OPEN on a stream channel")
		return
	}
	s := newStream(l, ch, f.ID, head.Kind, append(json.RawMessage(nil), f.Payload...), false)
	if !ch.control {
		ch.setStream(s)
	}
	l.streams[f.ID] = s
	handler := l.opts.Handlers[head.Kind]
	l.mu.Unlock()
	switch {
	case headErr != nil || head.Kind == "":
		_ = s.Reset("EPROTO: bad OPEN")
	case handler == nil:
		_ = s.Reset("EOPNOTSUPP: " + head.Kind)
	default:
		go l.serve(handler, s)
	}
}

func (l *Link) serve(h Handler, s *Stream) {
	defer func() {
		if r := recover(); r != nil {
			l.opts.Logf("link: %s handler panicked: %v", s.kind, r)
			_ = s.Reset("EIO: internal error")
		}
	}()
	h(s)
}

func (ch *channel) claim() bool {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.used {
		return false
	}
	ch.used = true
	return true
}

func (ch *channel) setStream(s *Stream) {
	ch.mu.Lock()
	ch.stream = s
	ch.mu.Unlock()
}

func (l *Link) release(s *Stream) {
	l.mu.Lock()
	if l.streams[s.id] == s {
		delete(l.streams, s.id)
	}
	ch := s.ch
	closeIt := false
	if !ch.control {
		ch.mu.Lock()
		if !ch.released {
			ch.released = true
			closeIt = true
			if ch.own {
				l.ownChans--
			}
		}
		ch.mu.Unlock()
	}
	l.mu.Unlock()
	if closeIt {
		_ = ch.dc.Close()
	}
}

func (l *Link) Open(ctx context.Context, open any, opts OpenOptions) (*Stream, error) {
	payload, err := json.Marshal(open)
	if err != nil {
		return nil, err
	}
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(payload, &head); err != nil || head.Kind == "" {
		return nil, errors.New("link: OPEN needs a kind")
	}
	select {
	case <-l.helloCh:
	case <-l.done:
		return nil, l.Err()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ch, id, err := l.openChannel(ctx, opts.Shared)
	if err != nil {
		return nil, err
	}
	s := newStream(l, ch, id, head.Kind, payload, true)
	l.mu.Lock()
	if l.err != nil {
		l.mu.Unlock()
		return nil, l.err
	}
	l.streams[id] = s
	l.mu.Unlock()
	if !ch.control {
		ch.setStream(s)
	}
	if err := s.send(FrameOpen, payload); err != nil {
		s.abort(&ResetError{Code: "ECONNRESET", Message: err.Error()})
		l.release(s)
		return nil, err
	}
	select {
	case <-s.answered:
	case <-ctx.Done():
		_ = s.Reset("ECANCELED")
		return nil, ctx.Err()
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return s, nil
}

func (l *Link) openChannel(ctx context.Context, shared bool) (*channel, uint32, error) {
	l.mu.Lock()
	id := l.nextID
	l.nextID += 2
	if shared || l.ownChans >= l.opts.MaxChannels {
		ch := l.control
		l.mu.Unlock()
		return ch, id, nil
	}
	l.ownChans++
	l.mu.Unlock()
	dc, err := l.pc.CreateDataChannel(StreamName, nil)
	if err != nil {
		l.mu.Lock()
		l.ownChans--
		l.mu.Unlock()
		return nil, 0, err
	}
	ch := &channel{l: l, dc: dc, own: true, used: true}
	opened := make(chan struct{})
	var once sync.Once
	dc.OnOpen(func() { once.Do(func() { close(opened) }) })
	l.wireChannel(ch)
	if dc.ReadyState() == webrtc.DataChannelStateOpen {
		once.Do(func() { close(opened) })
	}
	select {
	case <-opened:
		return ch, id, nil
	case <-l.done:
		err = l.Err()
	case <-ctx.Done():
		err = ctx.Err()
	}
	l.mu.Lock()
	l.ownChans--
	ch.released = true
	l.mu.Unlock()
	_ = dc.Close()
	return nil, 0, err
}

type idSet struct {
	floor uint32
	seen  map[uint32]struct{}
}

func (s *idSet) add(id uint32) bool {
	if id < s.floor {
		return false
	}
	if _, ok := s.seen[id]; ok {
		return false
	}
	if s.seen == nil {
		s.seen = map[uint32]struct{}{}
	}
	s.seen[id] = struct{}{}
	for {
		if _, ok := s.seen[s.floor]; !ok {
			return true
		}
		delete(s.seen, s.floor)
		s.floor += 2
	}
}
