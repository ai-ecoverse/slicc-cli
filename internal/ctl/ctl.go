package ctl

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/flock"
)

const (
	version     = 1
	maxRequest  = 4 << 10
	maxResponse = 1 << 20
	ioTimeout   = 2 * time.Second
	maxSockPath = 100
)

const V = version

const (
	OpStatus  = "status"
	OpDetach  = "detach"
	OpStop    = "stop"
	OpRevoke  = "revoke"
	OpLeases  = "leases"
	OpUnseal  = "unseal"
	OpSealOff = "seal-off"
	OpSealOn  = "seal-on"
)

var Ops = []string{OpStatus, OpDetach, OpStop, OpRevoke, OpLeases, OpUnseal, OpSealOff, OpSealOn}

const (
	StateWaiting      = "waiting"
	StateDialing      = "dialing"
	StateOffer        = "waiting for offer"
	StateConnecting   = "connecting"
	StateConnected    = "connected"
	StateReconnecting = "reconnecting"
	StateStopped      = "stopped"
)

type ScreenStatus struct {
	V                     int    `json:"v"`
	Available             bool   `json:"available"`
	ProviderAuthenticated bool   `json:"providerAuthenticated"`
	Granted               bool   `json:"granted"`
	Reason                string `json:"reason"`
}

type CDPStatus struct {
	Port    int `json:"port"`
	Clients int `json:"clients"`
}

type Status struct {
	Screen     *ScreenStatus `json:"screen,omitempty"`
	CDP        *CDPStatus    `json:"cdp,omitempty"`
	V          int           `json:"v"`
	PID        int           `json:"pid"`
	Mode       string        `json:"mode"`
	Version    string        `json:"version"`
	Started    time.Time     `json:"started"`
	Seven      string        `json:"seven,omitempty"`
	Origin     string        `json:"origin,omitempty"`
	Hub        string        `json:"hub,omitempty"`
	Device     string        `json:"device,omitempty"`
	State      string        `json:"state"`
	Since      time.Time     `json:"since"`
	Attempt    int           `json:"attempt,omitempty"`
	RetryAt    *time.Time    `json:"retryAt,omitempty"`
	Code       string        `json:"code,omitempty"`
	Error      string        `json:"error,omitempty"`
	Offers     []string      `json:"offers"`
	Ops        []string      `json:"ops,omitempty"`
	Peers      []string      `json:"peers,omitempty"`
	Dials      int           `json:"dials"`
	Links      int           `json:"links"`
	LastLinked *time.Time    `json:"lastLinked,omitempty"`
}

type Holder struct {
	mu     sync.Mutex
	s      Status
	ops    map[string]func()
	calls  map[string]func(Request) map[string]any
	slow   map[string]time.Duration
	peers  func() []string
	screen func() ScreenStatus
}

func NewHolder(mode, ver string) *Holder {
	now := time.Now().UTC()
	return &Holder{s: Status{V: version, PID: os.Getpid(), Mode: mode, Version: ver, Started: now, State: StateWaiting, Since: now, Offers: []string{}}}
}

func (h *Holder) Update(f func(*Status)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.s.State
	f(&h.s)
	if h.s.State != state {
		h.s.Since = time.Now().UTC()
	}
}

func (h *Holder) SetState(state string) { h.Update(func(s *Status) { s.State = state }) }

func (h *Holder) Snapshot() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.s
	if h.screen != nil {
		screen := h.screen()
		s.Screen = &screen
	}
	s.Offers = append([]string{}, h.s.Offers...)
	if h.s.CDP != nil {
		c := *h.s.CDP
		s.CDP = &c
	}
	s.Ops = []string{OpStatus}
	for _, op := range Ops {
		if h.ops[op] != nil || h.calls[op] != nil {
			s.Ops = append(s.Ops, op)
		}
	}
	if h.peers != nil {
		s.Peers = h.peers()
	}
	return s
}

func (h *Holder) Peers(f func() []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.peers = f
}

func (h *Holder) Answer(op string, f func(Request) map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.calls == nil {
		h.calls = map[string]func(Request) map[string]any{}
	}
	h.calls[op] = f
}

func (h *Holder) AnswerWithin(op string, d time.Duration, f func(Request) map[string]any) {
	h.Answer(op, f)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.slow == nil {
		h.slow = map[string]time.Duration{}
	}
	h.slow[op] = d
}

func (h *Holder) within(op string) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.slow[op]
}

func (h *Holder) call(op string) func(Request) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls[op]
}

func (h *Holder) Handle(op string, f func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ops == nil {
		h.ops = map[string]func(){}
	}
	h.ops[op] = f
}

func (h *Holder) handler(op string) func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ops[op]
}

type Request struct {
	V           int    `json:"v"`
	Op          string `json:"op"`
	Fingerprint string `json:"fingerprint,omitempty"`
	LeaseID     string `json:"leaseId,omitempty"`
}

type Server struct {
	ln   net.Listener
	lock *os.File
	path string
	h    *Holder
	wg   sync.WaitGroup
	once sync.Once
}

func Serve(h *Holder) (*Server, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, strconv.Itoa(os.Getpid())+".sock")
	lock, err := holdLock(path+".lock", ioTimeout)
	if err != nil {
		return nil, err
	}
	if lock == nil {
		return nil, fmt.Errorf("%s is locked", path)
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		flock.Unlock(lock)
		_ = lock.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	s := &Server{ln: ln, lock: lock, path: path, h: h}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

func holdLock(path string, wait time.Duration) (*os.File, error) {
	for range 5 {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		if !lockWithin(f, wait) {
			_ = f.Close()
			return nil, nil
		}
		if current(f, path) {
			return f, nil
		}
		flock.Unlock(f)
		_ = f.Close()
	}
	return nil, fmt.Errorf("%s keeps changing", path)
}

func current(f *os.File, path string) bool {
	held, err := f.Stat()
	if err != nil {
		return false
	}
	now, err := os.Stat(path)
	return err == nil && os.SameFile(held, now)
}

func lockWithin(f *os.File, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		ok, err := flock.TryLock(f)
		if ok {
			return true
		}
		if err != nil || time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *Server) Path() string { return s.path }

func (s *Server) Close() {
	s.once.Do(func() {
		_ = s.ln.Close()
		s.wg.Wait()
		if err := os.Remove(s.path); err == nil || errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(s.path + ".lock")
		}
		flock.Unlock(s.lock)
		_ = s.lock.Close()
	})
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.answer(c)
		}()
	}
}

func (s *Server) answer(c net.Conn) {
	defer func() { _ = c.Close() }()
	if !peerIsUs(c) {
		return
	}
	_ = c.SetDeadline(time.Now().Add(ioTimeout))
	line, err := bufio.NewReader(io.LimitReader(c, maxRequest)).ReadString('\n')
	if err != nil {
		return
	}
	var req Request
	var out any
	var act func()
	switch {
	case json.Unmarshal([]byte(line), &req) != nil || req.V != version:
		out = map[string]any{"v": version, "error": "EPROTO"}
	case req.Op == OpStatus:
		out = s.h.Snapshot()
	case s.h.call(req.Op) != nil:
		if d := s.h.within(req.Op); d > 0 {
			_ = c.SetDeadline(time.Now().Add(d))
		}
		res := s.h.call(req.Op)(req)
		res["v"] = version
		out = res
	case s.h.handler(req.Op) != nil:
		out, act = map[string]any{"v": version, "ok": true}, s.h.handler(req.Op)
	default:
		out = map[string]any{"v": version, "error": "EINVAL"}
	}
	body, _ := json.Marshal(out)
	_, _ = c.Write(append(body, '\n'))
	if act != nil {
		_ = c.Close()
		go act()
	}
}

type Entry struct {
	Path   string
	PID    int
	Status *Status
	Err    error
}

var ErrStale = errors.New("stale: nothing answers")

func List(ctx context.Context) ([]Entry, error) {
	dirs, err := searchDirs()
	if err != nil {
		return nil, err
	}
	var paths []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		if !ownDir(dir) {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "*.sock"))
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				paths = append(paths, m)
			}
		}
	}
	entries := make([]Entry, len(paths))
	var wg sync.WaitGroup
	for i, p := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pid, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(p), ".sock"))
			st, err := Query(ctx, p)
			entries[i] = Entry{Path: p, PID: pid, Status: st, Err: err}
		}()
	}
	wg.Wait()
	sort.Slice(entries, func(i, j int) bool { return entries[i].PID < entries[j].PID })
	return entries, nil
}

func Query(ctx context.Context, path string) (*Status, error) {
	ctx, cancel := context.WithTimeout(ctx, ioTimeout)
	defer cancel()
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		if refused(err) {
			return nil, ErrStale
		}
		return nil, err
	}
	defer func() { _ = c.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	body, _ := json.Marshal(Request{V: version, Op: OpStatus})
	if _, err := c.Write(append(body, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(io.LimitReader(c, maxResponse)).ReadBytes('\n')
	if err != nil {
		if len(line) == 0 && errors.Is(err, io.EOF) {
			return nil, errors.New("refused to answer")
		}
		return nil, err
	}
	var st Status
	if err := json.Unmarshal(line, &st); err != nil || st.V != version || st.PID == 0 {
		return nil, errors.New("sent a malformed status")
	}
	return &st, nil
}

type Reply struct {
	V      int    `json:"v"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Closed bool   `json:"closed,omitempty"`
}

func Send(ctx context.Context, path, op string) (*Reply, error) {
	return Call(ctx, path, Request{Op: op})
}

func Call(ctx context.Context, path string, req Request) (*Reply, error) {
	req.V = version
	ctx, cancel := context.WithTimeout(ctx, ioTimeout)
	defer cancel()
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	body, _ := json.Marshal(req)
	_, _ = c.Write(append(body, '\n'))
	line, err := bufio.NewReader(io.LimitReader(c, maxResponse)).ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var r Reply
	if err := json.Unmarshal(line, &r); err != nil || r.V != version {
		return nil, errors.New("sent a malformed reply")
	}
	return &r, nil
}

func Wipe(path string) (bool, error) {
	lock, err := holdLock(path+".lock", 0)
	if err != nil || lock == nil {
		return false, err
	}
	defer func() {
		flock.Unlock(lock)
		_ = lock.Close()
	}()
	if _, err := Query(context.Background(), path); !errors.Is(err, ErrStale) {
		return false, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	_ = os.Remove(path + ".lock")
	return true, nil
}

func (h *Holder) Screen(f func() ScreenStatus) { h.mu.Lock(); defer h.mu.Unlock(); h.screen = f }
