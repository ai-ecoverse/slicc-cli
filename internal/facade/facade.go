package facade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
)

const (
	EnvURL  = "SLICC_LINK_URL"
	version = 1
)

var (
	answerTimeout = time.Minute
	resetGrace    = time.Second
)

var (
	ErrNoURL  = errors.New(EnvURL + " isn't set, so this seven offers no link facade")
	ErrBadURL = errors.New(EnvURL + " isn't tcp://127.0.0.1:<port>")
	errLost   = errors.New("seven's link facade closed the connection")
)

type Conn interface {
	io.Reader
	io.Writer
	Close() error
}

type Dialer func(ctx context.Context, address string) (Conn, error)

type Facade struct {
	addr string
	join string
	dial Dialer
	done chan struct{}
	once sync.Once
}

func ParseURL(raw string) (string, error) {
	if raw == "" {
		return "", ErrNoURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "tcp" || u.User != nil || u.Hostname() == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", ErrBadURL
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || !loopback(u.Hostname()) {
		return "", ErrBadURL
	}
	return u.Host, nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() != nil && ip.IsLoopback()
}

func New(rawURL, join string, dial Dialer) (*Facade, error) {
	addr, err := ParseURL(rawURL)
	if err != nil {
		return nil, err
	}
	return &Facade{addr: addr, join: join, dial: dial, done: make(chan struct{})}, nil
}

func (f *Facade) Allowed() bool { return true }

func (f *Facade) Name() string { return "seven" }

func (f *Facade) Done() <-chan struct{} { return f.done }

func (f *Facade) lost() { f.once.Do(func() { close(f.done) }) }

type hello struct {
	V    int    `json:"v"`
	Join string `json:"join,omitempty"`
	Open any    `json:"open"`
}

type answer struct {
	Opened json.RawMessage `json:"opened"`
	Reset  *string         `json:"reset"`
}

func (f *Facade) Open(ctx context.Context, open any) (agentctl.Pipe, error) {
	c, opened, err := f.open(ctx, open, answerTimeout)
	if err != nil {
		return nil, err
	}
	return newPipe(c, opened), nil
}

func Call(ctx context.Context, rawURL string, open any, wait time.Duration, dial Dialer) (json.RawMessage, error) {
	f, err := New(rawURL, "", dial)
	if err != nil {
		return nil, err
	}
	c, opened, err := f.open(ctx, open, wait)
	if err != nil {
		return nil, err
	}
	_ = c.Close()
	return opened, nil
}

func (f *Facade) open(ctx context.Context, open any, wait time.Duration) (Conn, json.RawMessage, error) {
	body, err := json.Marshal(hello{V: version, Join: f.join, Open: open})
	if err != nil {
		return nil, nil, err
	}
	if len(body) > maxControl {
		return nil, nil, errors.New("the stream's opening doesn't fit in a facade control record")
	}
	hctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	c, err := f.dial(hctx, f.addr)
	if err != nil {
		f.lost()
		return nil, nil, fmt.Errorf("can't reach seven's link facade: %w", err)
	}
	stop := context.AfterFunc(hctx, func() { _ = c.Close() })
	opened, err := f.handshake(c, body)
	stopped := stop()
	if err == nil {
		return c, opened, nil
	}
	_ = c.Close()
	switch {
	case stopped || hctx.Err() == nil:
		return nil, nil, err
	case ctx.Err() != nil:
		return nil, nil, ctx.Err()
	}
	return nil, nil, fmt.Errorf("seven's link facade didn't answer within %s", wait)
}

func (f *Facade) handshake(c Conn, body []byte) (json.RawMessage, error) {
	if err := writeRecord(c, recControl, body); err != nil {
		f.lost()
		return nil, fmt.Errorf("can't reach seven's link facade: %w", err)
	}
	rec, err := readRecord(c)
	if err != nil {
		if errors.Is(err, errMalformed) {
			_ = sendReset(c, "EPROTO")
			return nil, &agentctl.ResetError{Code: "EPROTO", Message: "seven's link facade sent " + err.Error()}
		}
		f.lost()
		return nil, errLost
	}
	if rec.typ != recControl {
		_ = sendReset(c, "EPROTO")
		return nil, &agentctl.ResetError{Code: "EPROTO", Message: "seven's link facade sent data before it answered"}
	}
	var a answer
	if json.Unmarshal(rec.payload, &a) != nil || (a.Reset == nil && len(a.Opened) == 0) {
		_ = sendReset(c, "EPROTO")
		return nil, &agentctl.ResetError{Code: "EPROTO", Message: "seven's link facade sent a malformed answer"}
	}
	if a.Reset != nil {
		re := parseReset(*a.Reset)
		if re.Code == "ENOTCONN" {
			f.lost()
		}
		return nil, re
	}
	return a.Opened, nil
}

func parseReset(s string) *agentctl.ResetError {
	code, msg, _ := strings.Cut(s, ":")
	code = strings.TrimSpace(code)
	if code == "" {
		code = "EIO"
	}
	return &agentctl.ResetError{Code: code, Message: strings.TrimSpace(msg), Remote: true}
}

func sendReset(w io.Writer, reason string) error {
	body, _ := json.Marshal(struct {
		Reset string `json:"reset"`
	}{reason})
	return writeRecord(w, recControl, body)
}

type pipe struct {
	c      Conn
	opened json.RawMessage
	done   chan struct{}
	once   sync.Once
	items  chan []byte
	quit   chan struct{}

	smu     sync.Mutex
	failure error
	wclosed bool
	ended   bool

	rmu  sync.Mutex
	rbuf []byte

	umu sync.Mutex
	wmu sync.Mutex
}

func newPipe(c Conn, opened json.RawMessage) *pipe {
	p := &pipe{c: c, opened: opened, done: make(chan struct{}), items: make(chan []byte, 4), quit: make(chan struct{})}
	go p.read()
	return p
}

func (p *pipe) Opened() json.RawMessage { return p.opened }

func (p *pipe) Done() <-chan struct{} { return p.done }

func (p *pipe) finish() {
	p.once.Do(func() {
		_ = p.c.Close()
		close(p.done)
	})
}

func (p *pipe) fail(err error) {
	p.smu.Lock()
	if p.failure == nil {
		p.failure = err
	}
	p.smu.Unlock()
}

func (p *pipe) err() error {
	p.smu.Lock()
	defer p.smu.Unlock()
	return p.failure
}

func (p *pipe) protocol(msg string) {
	p.fail(&agentctl.ResetError{Code: "EPROTO", Message: "seven's link facade sent " + msg})
	sent := make(chan struct{})
	go func() {
		p.wmu.Lock()
		_ = sendReset(p.c, "EPROTO")
		p.wmu.Unlock()
		close(sent)
	}()
	select {
	case <-sent:
	case <-time.After(resetGrace):
	}
	p.finish()
}

func (p *pipe) record(typ byte, payload []byte) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	return writeRecord(p.c, typ, payload)
}

func (p *pipe) read() {
	defer close(p.quit)
	for {
		rec, err := readRecord(p.c)
		switch {
		case errors.Is(err, errMalformed):
			p.protocol(err.Error())
			return
		case err != nil:
			p.fail(errLost)
			p.finish()
			return
		case rec.typ == recData:
			if len(rec.payload) == 0 {
				continue
			}
			select {
			case p.items <- rec.payload:
			case <-p.done:
				p.fail(errLost)
				return
			}
		case rec.typ == recEnd:
			p.fail(io.EOF)
			p.smu.Lock()
			p.ended = true
			both := p.wclosed
			p.smu.Unlock()
			if both {
				p.finish()
			}
			return
		default:
			var a answer
			if json.Unmarshal(rec.payload, &a) != nil || a.Reset == nil {
				p.protocol("a control record that isn't a reset")
				return
			}
			p.fail(parseReset(*a.Reset))
			p.finish()
			return
		}
	}
}

func (p *pipe) Read(b []byte) (int, error) {
	p.rmu.Lock()
	defer p.rmu.Unlock()
	for len(p.rbuf) == 0 {
		select {
		case item := <-p.items:
			p.rbuf = item
		case <-p.quit:
			select {
			case item := <-p.items:
				p.rbuf = item
			default:
				return 0, p.err()
			}
		}
	}
	n := copy(b, p.rbuf)
	p.rbuf = p.rbuf[n:]
	return n, nil
}

func (p *pipe) lostOr() error {
	if err := p.err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return errLost
}

func (p *pipe) Write(b []byte) (int, error) {
	p.umu.Lock()
	defer p.umu.Unlock()
	p.smu.Lock()
	closed := p.wclosed
	p.smu.Unlock()
	if closed {
		return 0, errors.New("write after end")
	}
	written := 0
	for len(b) > 0 {
		if err := p.err(); err != nil && !errors.Is(err, io.EOF) {
			return written, err
		}
		n := min(len(b), maxData)
		if err := p.record(recData, b[:n]); err != nil {
			p.finish()
			return written, p.lostOr()
		}
		written += n
		b = b[n:]
	}
	return written, nil
}

func (p *pipe) CloseWrite() error {
	p.umu.Lock()
	p.smu.Lock()
	if p.wclosed {
		p.smu.Unlock()
		p.umu.Unlock()
		return nil
	}
	p.wclosed = true
	p.smu.Unlock()
	err := p.record(recEnd, nil)
	p.umu.Unlock()
	p.smu.Lock()
	both := p.ended
	p.smu.Unlock()
	if both || err != nil {
		p.finish()
	}
	if err != nil {
		return p.lostOr()
	}
	return nil
}

func (p *pipe) Reset(reason string) error {
	if err := p.err(); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	p.wmu.Lock()
	err := sendReset(p.c, reason)
	p.wmu.Unlock()
	p.smu.Lock()
	p.wclosed = true
	p.smu.Unlock()
	p.fail(&agentctl.ResetError{Code: reason})
	p.finish()
	return err
}
