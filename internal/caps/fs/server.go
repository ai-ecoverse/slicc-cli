package fs

import (
	"encoding/json"
	"errors"
	"os"
	"sync"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

const (
	Kind = "fs"
	Cap  = "fs"
)

type Server struct {
	ns   sync.RWMutex
	hub  *hub
	logf func(format string, args ...any)

	mu           sync.Mutex
	mounts       []*Mount
	byName       map[string]*Mount
	sessions     map[*link.Link]*session
	watches      map[*watchStream]bool
	pushers      map[*link.Link]*pusher
	version      int
	helloVersion int
	closed       bool
}

func New(mounts []Mount, logf func(format string, args ...any)) *Server {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		byName: map[string]*Mount{}, hub: newHub(startBackend), logf: logf,
		sessions: map[*link.Link]*session{}, watches: map[*watchStream]bool{}, pushers: map[*link.Link]*pusher{},
	}
	for i := range mounts {
		m := mounts[i]
		s.mounts = append(s.mounts, &m)
		s.byName[m.Name] = &m
	}
	return s
}

func Register(n *node.Node, mounts []Mount) *Server {
	if len(mounts) == 0 {
		return nil
	}
	s := New(mounts, n.Logf)
	n.Handle(Cap, Kind, s.Handle)
	n.OnHello(s.hello)
	n.Subscribe(func(e node.Event) {
		if e.Kind == node.EventLinked && e.Link != nil {
			s.Attach(e.Link)
		}
	})
	return s
}

func (s *Server) Hello() []link.Mount {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]link.Mount, 0, len(s.mounts))
	for _, m := range s.mounts {
		out = append(out, link.Mount{Name: m.Name, Readonly: m.ReadOnly, Capabilities: m.Capabilities})
	}
	return out
}

func (s *Server) Mounts() []Mount {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Mount, 0, len(s.mounts))
	for _, m := range s.mounts {
		out = append(out, *m)
	}
	return out
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.closed = true
	sessions := s.sessions
	s.sessions = map[*link.Link]*session{}
	s.mu.Unlock()
	for _, ss := range sessions {
		ss.close()
	}
	s.hub.close()
	return nil
}

func (s *Server) Handle(st *link.Stream) {
	var a args
	if err := json.Unmarshal(st.OpenPayload(), &a); err != nil || a == nil {
		_ = st.Reset(Reason(fail("EINVAL")))
		return
	}
	op, opOK := a.str("op")
	var err error
	if opOK && op == "watch" {
		err = s.watch(st, a)
	} else {
		err = s.dispatch(st, a, op, opOK)
	}
	if err != nil {
		var re *link.ResetError
		if errors.As(err, &re) || errors.Is(err, link.ErrWriteAfterEnd) {
			return
		}
		_ = st.Reset(Reason(err))
	}
}

func (s *Server) lookup(name string) *Mount {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byName[name]
}

func (s *Server) mount(a args) (*Mount, error) {
	name, _ := a.str("mount")
	m := s.lookup(name)
	if m == nil {
		return nil, fail("ENOENT", "no such folder")
	}
	return m, nil
}

func (s *Server) dispatch(st *link.Stream, a args, op string, opOK bool) error {
	m, err := s.mount(a)
	if err != nil {
		return err
	}
	if !opOK {
		return fail("EINVAL")
	}
	readonly := m.ReadOnly || a.truthy("readonly")
	switch op {
	case "read":
		return s.read(st, m, a)
	case "write":
		return s.write(st, m, a, readonly)
	case "list":
		return s.list(st, m, a)
	}
	answer, err := s.call(st.Link(), m, op, a, readonly)
	if err != nil {
		return err
	}
	if err := st.Accept(answer); err != nil {
		return err
	}
	return st.CloseWrite()
}

var writingOps = map[string]bool{"mkdir": true, "rmdir": true, "unlink": true, "rename": true, "symlink": true, "setattr": true}

func (s *Server) call(l *link.Link, m *Mount, op string, a args, readonly bool) (any, error) {
	writes := writingOps[op] || (op == "open" && a.wantsWrite())
	if writes && readonly {
		return nil, fail("EROFS")
	}
	switch op {
	case "open":
		return s.open(l, m, a, writes)
	case "release":
		return s.release(l, m, a)
	}
	if writes {
		s.ns.Lock()
		defer s.ns.Unlock()
		p, _ := a.str("path")
		if p == "" {
			p, _ = a.str("from")
		}
		s.logf("fs %s %s/%s", op, m.Name, p)
	} else {
		s.ns.RLock()
		defer s.ns.RUnlock()
	}
	return pathOp(m, op, a)
}

type session struct {
	mu      sync.Mutex
	next    uint64
	handles map[uint64]*handle
	count   map[*Mount]int
	closed  bool
}

type handle struct {
	m    *Mount
	path string
	file *os.File
}

func (s *Server) session(l *link.Link) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fail("EBADF")
	}
	if ss := s.sessions[l]; ss != nil {
		return ss, nil
	}
	ss := &session{handles: map[uint64]*handle{}, count: map[*Mount]int{}}
	s.sessions[l] = ss
	if l != nil {
		go func() {
			<-l.Done()
			s.mu.Lock()
			if s.sessions[l] == ss {
				delete(s.sessions, l)
			}
			s.mu.Unlock()
			ss.close()
		}()
	}
	return ss, nil
}

func (ss *session) add(h *handle) (uint64, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		return 0, fail("EBADF")
	}
	if ss.count[h.m] >= MaxHandles {
		return 0, fail("EMFILE", "too many open files")
	}
	ss.next++
	ss.handles[ss.next] = h
	ss.count[h.m]++
	return ss.next, nil
}

func (ss *session) get(fh uint64, m *Mount, take bool) (*handle, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	h := ss.handles[fh]
	if h == nil || h.m != m {
		return nil, fail("EBADF")
	}
	if take {
		delete(ss.handles, fh)
		ss.count[m]--
	}
	return h, nil
}

func (ss *session) close() {
	ss.mu.Lock()
	handles := ss.handles
	ss.handles = map[uint64]*handle{}
	ss.count = map[*Mount]int{}
	ss.closed = true
	ss.mu.Unlock()
	for _, h := range handles {
		if h.file != nil {
			_ = h.file.Close()
		}
	}
}

func (s *Server) handleOf(l *link.Link, m *Mount, a args, take bool) (*handle, error) {
	fh, err := a.fh()
	if err != nil {
		return nil, err
	}
	ss, err := s.session(l)
	if err != nil {
		return nil, err
	}
	return ss.get(fh, m, take)
}
