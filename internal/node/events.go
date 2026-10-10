package node

import (
	"sync"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

type EventKind string

const (
	EventConnecting  EventKind = "connecting"
	EventLinked      EventKind = "linked"
	EventLinkDown    EventKind = "link-down"
	EventStreamOpen  EventKind = "stream-open"
	EventStreamClose EventKind = "stream-close"
)

type Event struct {
	Kind   EventKind
	Link   *link.Link
	Peer   link.Hello
	Stream string
	Reason string
	Err    error
}

type subscribers struct {
	mu   sync.RWMutex
	next int
	subs map[int]func(Event)
	ids  []int
}

func (n *Node) Subscribe(f func(Event)) func() {
	s := &n.subs
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subs == nil {
		s.subs = map[int]func(Event){}
	}
	s.next++
	id := s.next
	s.subs[id] = f
	s.ids = append(s.ids, id)
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(s.subs, id)
			for i, v := range s.ids {
				if v == id {
					s.ids = append(s.ids[:i:i], s.ids[i+1:]...)
					break
				}
			}
		})
	}
}

func (n *Node) emit(e Event) {
	s := &n.subs
	s.mu.RLock()
	fs := make([]func(Event), 0, len(s.ids))
	for _, id := range s.ids {
		fs = append(fs, s.subs[id])
	}
	s.mu.RUnlock()
	for _, f := range fs {
		f(e)
	}
}

func (n *Node) subscribed() bool {
	n.subs.mu.RLock()
	defer n.subs.mu.RUnlock()
	return len(n.subs.ids) > 0
}

func (n *Node) counted(kind string, h link.Handler) link.Handler {
	return func(s *link.Stream) {
		if n.subscribed() {
			n.emit(Event{Kind: EventStreamOpen, Link: s.Link(), Stream: kind})
			go func() {
				<-s.Done()
				n.emit(Event{Kind: EventStreamClose, Link: s.Link(), Stream: kind, Err: s.Err()})
			}()
		}
		h(s)
	}
}
