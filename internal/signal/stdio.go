package signal

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

type Stdio struct {
	w   io.Writer
	wmu sync.Mutex

	mu    sync.Mutex
	nonce string

	in   chan Message
	err  error
	done chan struct{}
	once sync.Once
}

func NewStdio(r io.Reader, w io.Writer) (*Stdio, error) {
	s := &Stdio{w: w, in: make(chan Message, 64), done: make(chan struct{})}
	if err := s.rehello(); err != nil {
		return nil, err
	}
	go s.read(r)
	return s, nil
}

func (s *Stdio) Nonce() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nonce
}

func (s *Stdio) rehello() error {
	nonce := NewNonce()
	s.mu.Lock()
	s.nonce = nonce
	s.mu.Unlock()
	return s.write(Hello(nonce))
}

func (s *Stdio) write(m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err = s.w.Write(b)
	return err
}

func (s *Stdio) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		m, err := Parse(line)
		if err != nil {
			continue
		}
		if m.T == TypeHello {
			if err := s.rehello(); err != nil {
				s.stop(err)
				return
			}
			continue
		}
		if m.Nonce != s.Nonce() {
			continue
		}
		select {
		case s.in <- m:
		case <-s.done:
			return
		}
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	s.stop(err)
}

func (s *Stdio) stop(err error) {
	s.once.Do(func() {
		s.err = err
		close(s.done)
	})
}

func (s *Stdio) Recv(ctx context.Context) (Message, error) {
	select {
	case m := <-s.in:
		return m, nil
	case <-s.done:
		select {
		case m := <-s.in:
			return m, nil
		default:
		}
		return Message{}, s.err
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}

func (s *Stdio) Send(_ context.Context, m Message) error {
	if m.Nonce != s.Nonce() {
		return nil
	}
	switch m.T {
	case TypeAnswer, TypeCandidate, TypeBye:
	default:
		return fmt.Errorf("signal: the node does not send %q", m.T)
	}
	return s.write(m)
}

func (s *Stdio) Close() error {
	s.stop(ErrClosed)
	return nil
}

var _ Conn = (*Stdio)(nil)
