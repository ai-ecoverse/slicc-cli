package clipboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	hostclip "github.com/ai-ecoverse/slicc-cli/internal/clipboard"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

const (
	Kind    = "clipboard"
	Cap     = "clipboard"
	ReadCap = "clipboard-read"
	Idle    = 30 * time.Second
)

type Options struct {
	Clipboard *hostclip.Clipboard
	Read      bool
	Idle      time.Duration
	Logf      func(format string, args ...any)
	Status    func(format string, args ...any)
}

type Server struct {
	o  Options
	mu sync.Mutex
}

func Register(n *node.Node, o Options) *Server {
	if o.Clipboard == nil {
		return nil
	}
	if o.Logf == nil {
		o.Logf = n.Logf
	}
	if o.Status == nil {
		o.Status = n.Notice
	}
	s := New(o)
	n.Handle(Cap, Kind, s.Handle)
	if o.Read {
		n.Advertise(ReadCap)
	}
	n.OnHello(func(h *link.Hello) { h.Clipboard = &link.Clipboard{Backend: o.Clipboard.Name} })
	return s
}

func New(o Options) *Server {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Status == nil {
		o.Status = func(string, ...any) {}
	}
	if o.Idle <= 0 {
		o.Idle = Idle
	}
	return &Server{o: o}
}

func (s *Server) Handle(st *link.Stream) {
	var req struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(st.OpenPayload(), &req); err != nil {
		_ = st.Reset("EPROTO: malformed clipboard OPEN")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-st.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	switch req.Op {
	case "read":
		s.read(ctx, st)
	case "write":
		s.write(ctx, st)
	default:
		_ = st.Reset("EPROTO: op must be read or write")
	}
}

func peer(st *link.Stream) string {
	if h, _ := st.Link().PeerHello(); h.Name != "" {
		return h.Name
	}
	return "seven"
}

func (s *Server) read(ctx context.Context, st *link.Stream) {
	if !s.o.Read {
		_ = st.Reset("EACCES: this node doesn't offer clipboard-read")
		return
	}
	go func() { _, _ = io.Copy(io.Discard, st) }()
	s.mu.Lock()
	text, err := s.o.Clipboard.Read(ctx)
	s.mu.Unlock()
	if err != nil {
		s.o.Logf("clipboard: read: %v", err)
		_ = st.Reset(err.Error())
		return
	}
	s.o.Status("clipboard: %q read this machine's clipboard", peer(st))
	_ = st.Accept(nil)
	_ = st.SetWriteDeadline(time.Now().Add(s.o.Idle))
	if _, err := st.Write([]byte(text)); err != nil {
		_ = st.Reset("ETIMEDOUT: seven didn't take the text")
		return
	}
	_ = st.CloseWrite()
}

func (s *Server) write(ctx context.Context, st *link.Stream) {
	_ = st.Accept(nil)
	_ = st.SetReadDeadline(time.Now().Add(s.o.Idle))
	text, err := io.ReadAll(io.LimitReader(st, hostclip.MaxBytes+1))
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		_ = st.Reset("ETIMEDOUT: the text didn't arrive")
		return
	case err != nil:
		return
	case len(text) > hostclip.MaxBytes:
		_ = st.Reset("EFBIG: the text is over 1 MiB")
		return
	}
	s.mu.Lock()
	err = s.o.Clipboard.Write(ctx, text)
	s.mu.Unlock()
	if err != nil {
		s.o.Logf("clipboard: write: %v", err)
		_ = st.Reset(err.Error())
		return
	}
	s.o.Logf("clipboard: write, %d bytes", len(text))
	_ = st.CloseWrite()
}
