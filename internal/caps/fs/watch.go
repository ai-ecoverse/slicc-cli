package fs

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

const (
	WatchDebounce = 50 * time.Millisecond
	WatchMaxPaths = 256
	WatchRestart  = time.Second
	WatchPing     = 15 * time.Second
	WatchLinger   = 5 * time.Second
	watchBacklog  = 64
)

type event struct {
	paths []string
	all   bool
}

type backend func(root string, note func(rel string, known bool), lost func()) (stop func(), err error)

type hub struct {
	start   backend
	mu      sync.Mutex
	folders map[string]*folder
}

type folder struct {
	h         *hub
	root      string
	listeners map[int]func(event)
	nextID    int
	pending   []string
	seen      map[string]bool
	all       bool
	timer     *time.Timer
	retry     *time.Timer
	linger    *time.Timer
	stop      func()
	lost      bool
	gen       int
	closed    bool
	ready     chan struct{}
}

func newHub(start backend) *hub {
	return &hub{start: start, folders: map[string]*folder{}}
}

func (h *hub) subscribe(root string, fn func(event)) func() {
	h.mu.Lock()
	f := h.folders[root]
	if f == nil {
		f = &folder{h: h, root: root, listeners: map[int]func(event){}, seen: map[string]bool{}, ready: make(chan struct{})}
		h.folders[root] = f
		f.startLocked()
	}
	if f.linger != nil {
		f.linger.Stop()
		f.linger = nil
	}
	f.nextID++
	id := f.nextID
	f.listeners[id] = fn
	ready := f.ready
	h.mu.Unlock()
	<-ready
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			delete(f.listeners, id)
			if len(f.listeners) == 0 && !f.closed {
				f.linger = time.AfterFunc(WatchLinger, func() {
					h.mu.Lock()
					defer h.mu.Unlock()
					if len(f.listeners) == 0 && !f.closed {
						f.closeLocked()
					}
				})
			}
		})
	}
}

func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, f := range h.folders {
		f.closeLocked()
	}
}

func (f *folder) closeLocked() {
	f.closed = true
	f.gen++
	if f.h.folders[f.root] == f {
		delete(f.h.folders, f.root)
	}
	for _, t := range []*time.Timer{f.timer, f.retry, f.linger} {
		if t != nil {
			t.Stop()
		}
	}
	if f.stop != nil {
		go f.stop()
		f.stop = nil
	}
	f.closeReady()
}

func (f *folder) closeReady() {
	select {
	case <-f.ready:
	default:
		close(f.ready)
	}
}

func (f *folder) startLocked() {
	f.gen++
	gen := f.gen
	note := func(rel string, known bool) {
		if known && fenced().covers(filepath.Join(f.root, filepath.FromSlash(rel))) {
			return
		}
		f.h.mu.Lock()
		defer f.h.mu.Unlock()
		if f.gen == gen && !f.closed {
			f.noteLocked(rel, known)
		}
	}
	lost := func() {
		f.h.mu.Lock()
		defer f.h.mu.Unlock()
		if f.gen == gen && !f.closed {
			f.gen++
			f.lostLocked()
		}
	}
	go func() {
		stop, err := f.h.start(f.root, note, lost)
		f.h.mu.Lock()
		defer f.h.mu.Unlock()
		defer f.closeReady()
		if f.gen != gen || f.closed {
			if stop != nil {
				go stop()
			}
			return
		}
		if err != nil {
			f.gen++
			f.lostLocked()
			return
		}
		f.stop = stop
		if f.lost {
			f.noteLocked("", false)
		}
		f.lost = false
	}()
}

func (f *folder) lostLocked() {
	if f.stop != nil {
		go f.stop()
		f.stop = nil
	}
	if !f.lost {
		f.noteLocked("", false)
	}
	f.lost = true
	f.retry = time.AfterFunc(WatchRestart, func() {
		f.h.mu.Lock()
		defer f.h.mu.Unlock()
		if !f.closed {
			f.startLocked()
		}
	})
}

func (f *folder) add(p string) {
	if !f.seen[p] {
		f.seen[p] = true
		f.pending = append(f.pending, p)
	}
}

func (f *folder) noteLocked(rel string, known bool) {
	if !known {
		f.all = true
	} else if !f.all {
		f.add(rel)
		if at := strings.LastIndex(rel, "/"); at >= 0 {
			f.add(rel[:at])
		} else {
			f.add("")
		}
		if len(f.pending) > WatchMaxPaths {
			f.all = true
		}
	}
	if f.all {
		f.pending, f.seen = nil, map[string]bool{}
	}
	if f.timer == nil {
		f.timer = time.AfterFunc(WatchDebounce, f.flush)
	}
}

func (f *folder) flush() {
	f.h.mu.Lock()
	defer f.h.mu.Unlock()
	f.timer = nil
	if f.closed {
		return
	}
	var ev event
	switch {
	case f.all:
		ev.all = true
	case len(f.pending) > 0:
		ev.paths = f.pending
	default:
		return
	}
	f.pending, f.seen, f.all = nil, map[string]bool{}, false
	for _, l := range f.listeners {
		l(ev)
	}
}

type watchLine struct {
	Mount string   `json:"mount,omitempty"`
	Paths []string `json:"paths,omitempty"`
	All   bool     `json:"all,omitempty"`
	Ping  int      `json:"ping,omitempty"`
}

type watchStream struct {
	st     *link.Stream
	mounts []*Mount
	mu     sync.Mutex
	queue  []watchLine
	wake   chan struct{}
	done   chan struct{}
	once   sync.Once
	reset  string
}

func (w *watchStream) covers(m *Mount) bool {
	for _, x := range w.mounts {
		if x == m {
			return true
		}
	}
	return false
}

func (w *watchStream) push(l watchLine) {
	w.mu.Lock()
	w.queue = append(w.queue, l)
	if len(w.queue) > watchBacklog {
		w.queue = fold(w.queue)
	}
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func fold(queue []watchLine) []watchLine {
	var out []watchLine
	seen := map[string]bool{}
	for _, l := range queue {
		if l.Ping != 0 || seen[l.Mount] {
			continue
		}
		seen[l.Mount] = true
		out = append(out, watchLine{Mount: l.Mount, All: true})
	}
	return out
}

func (w *watchStream) stop(reason string) {
	w.once.Do(func() {
		w.mu.Lock()
		w.reset = reason
		w.mu.Unlock()
		close(w.done)
	})
}

func (w *watchStream) fail(reason string) {
	w.stop(reason)
	_ = w.st.Reset(reason)
}

func (w *watchStream) take() []watchLine {
	w.mu.Lock()
	defer w.mu.Unlock()
	q := w.queue
	w.queue = nil
	return q
}

func (w *watchStream) run() {
	ping := time.NewTicker(WatchPing)
	defer ping.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-ping.C:
			w.push(watchLine{Ping: 1})
		case <-w.wake:
			for _, l := range w.take() {
				b, _ := json.Marshal(l)
				if _, err := w.st.Write(append(b, '\n')); err != nil {
					w.stop("")
					return
				}
			}
		}
	}
}

func (s *Server) watchMounts(a args) ([]*Mount, error) {
	var names []json.RawMessage
	if !a.has("mounts") || json.Unmarshal(a["mounts"], &names) != nil || len(names) == 0 {
		return nil, fail("EINVAL")
	}
	var mounts []*Mount
	for _, raw := range names {
		var name string
		if json.Unmarshal(raw, &name) != nil {
			return nil, fail("EINVAL")
		}
		m := s.lookup(name)
		if m == nil {
			return nil, fail("ENOENT", "no such folder")
		}
		dup := false
		for _, x := range mounts {
			dup = dup || x == m
		}
		if !dup {
			mounts = append(mounts, m)
		}
	}
	return mounts, nil
}

func (s *Server) watch(st *link.Stream, a args) error {
	mounts, err := s.watchMounts(a)
	if err != nil {
		return err
	}
	w := &watchStream{st: st, mounts: mounts, wake: make(chan struct{}, 1), done: make(chan struct{})}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fail("EBADF")
	}
	s.watches[w] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.watches, w)
		s.mu.Unlock()
	}()
	var unsubs []func()
	for _, m := range mounts {
		name := m.Name
		unsubs = append(unsubs, s.hub.subscribe(m.Root, func(ev event) {
			w.push(watchLine{Mount: name, Paths: ev.paths, All: ev.all})
		}))
	}
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()
	if err := st.Accept(nil); err != nil {
		return err
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := st.Read(buf); err != nil {
				w.stop("")
				return
			}
		}
	}()
	go func() {
		select {
		case <-st.Done():
			w.stop("")
		case <-w.done:
		}
	}()
	w.run()
	w.mu.Lock()
	reason := w.reset
	w.mu.Unlock()
	if reason == "" {
		_ = st.CloseWrite()
	}
	return nil
}
