package jerk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	hostjerk "github.com/ai-ecoverse/slicc-cli/internal/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

const (
	Kind                 = "jerk"
	Cap                  = "jerk"
	SealCap              = "jerk-seal"
	DefaultCreateTimeout = 2*time.Minute + 30*time.Second
	maxParams            = 16
	maxValue             = 256
)

var (
	paramKeyRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	scopeRE    = regexp.MustCompile(`^[\x21-\x7e]+( [\x21-\x7e]+)*$`)
)

type Options struct {
	Jerk          *hostjerk.Jerk
	Seal          bool
	Origin        func(*link.Link) string
	CreateTimeout time.Duration
	Timeout       time.Duration
	Logf          func(format string, args ...any)
}

type Server struct {
	o        Options
	mu       sync.Mutex
	creating map[*link.Link]bool
}

type open struct {
	Op       string            `json:"op"`
	Provider *string           `json:"provider"`
	Scope    *string           `json:"scope"`
	LeaseID  *string           `json:"lease_id"`
	Duration *float64          `json:"duration"`
	Params   map[string]string `json:"params"`
}

type protoError string

func (e protoError) Error() string { return string(e) }

func Register(n *node.Node, o Options) *Server {
	if o.Jerk == nil {
		return nil
	}
	if o.Logf == nil {
		o.Logf = n.Logf
	}
	if o.Origin == nil {
		o.Origin = n.PeerOrigin
	}
	s := New(o)
	n.Handle(Cap, Kind, s.Handle)
	if o.Seal {
		n.Advertise(SealCap)
	}
	n.OnHello(func(h *link.Hello) { h.Jerk = &link.Jerk{Version: o.Jerk.Version} })
	return s
}

func New(o Options) *Server {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.CreateTimeout <= 0 {
		o.CreateTimeout = DefaultCreateTimeout
	}
	if o.Timeout <= 0 {
		o.Timeout = hostjerk.Timeout
	}
	return &Server{o: o, creating: map[*link.Link]bool{}}
}

func (s *Server) Handle(st *link.Stream) {
	var req open
	if err := json.Unmarshal(st.OpenPayload(), &req); err != nil {
		_ = st.Reset("EPROTO: malformed jerk OPEN")
		return
	}
	call, err := s.call(st, req)
	if err != nil {
		var pe protoError
		if errors.As(err, &pe) {
			_ = st.Reset("EPROTO: " + pe.Error())
		} else {
			_ = st.Reset(err.Error())
		}
		return
	}
	if req.Op == "create" {
		if !s.claim(st.Link()) {
			_ = st.Reset("EBUSY: a create is already in flight on this link")
			return
		}
		defer s.release(st.Link())
	}
	s.run(st, req, call)
}

func (s *Server) claim(l *link.Link) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creating[l] {
		return false
	}
	s.creating[l] = true
	return true
}

func (s *Server) release(l *link.Link) {
	s.mu.Lock()
	delete(s.creating, l)
	s.mu.Unlock()
}

func provider(req open) (string, error) {
	if req.Provider == nil || !hostjerk.ValidProvider(*req.Provider) {
		return "", protoError(req.Op + " needs a provider matching ^[a-z0-9][a-z0-9-]{0,63}$")
	}
	return *req.Provider, nil
}

func (s *Server) call(st *link.Stream, req open) (hostjerk.Call, error) {
	switch req.Op {
	case "list":
		return hostjerk.Call{Args: []string{"list", "--json"}}, nil
	case "status", "accounts":
		p, err := provider(req)
		if err != nil {
			return hostjerk.Call{}, err
		}
		if req.Op == "status" {
			return hostjerk.Call{Args: []string{p, "get", "--json"}}, nil
		}
		return hostjerk.Call{Args: []string{p, "list", "--json"}}, nil
	case "create":
		return s.create(st, req)
	case "seal", "unseal":
		return s.seal(req)
	case "drop":
		return hostjerk.Call{}, errors.New("EACCES: drop isn't an op over the link; unseal drops what jerk minted")
	}
	return hostjerk.Call{}, protoError("unknown op")
}

func (s *Server) create(st *link.Stream, req open) (hostjerk.Call, error) {
	p, err := provider(req)
	if err != nil {
		return hostjerk.Call{}, err
	}
	args := []string{p, "create", "--via-link"}
	if origin := s.o.Origin(st.Link()); origin != "" {
		args = append(args, "--seven-origin", origin)
	}
	if req.Scope != nil {
		if !scopeRE.MatchString(*req.Scope) || len(*req.Scope) > maxValue {
			return hostjerk.Call{}, protoError("scope must be printable words")
		}
		args = append(args, "--scope", *req.Scope)
	}
	if len(req.Params) > maxParams {
		return hostjerk.Call{}, protoError("too many params")
	}
	for _, k := range sortedKeys(req.Params) {
		v := req.Params[k]
		if !paramKeyRE.MatchString(k) || len(v) > maxValue || !printable(v) {
			return hostjerk.Call{}, protoError("param " + strconv.Quote(k) + " isn't a key=value of printable text")
		}
		args = append(args, "-p", k+"="+v)
	}
	return hostjerk.Call{Args: args, Timeout: s.o.CreateTimeout}, nil
}

func (s *Server) seal(req open) (hostjerk.Call, error) {
	if !s.o.Seal {
		return hostjerk.Call{}, errors.New("EACCES: this node doesn't offer jerk-seal")
	}
	p, err := provider(req)
	if err != nil {
		return hostjerk.Call{}, err
	}
	if req.Op == "unseal" {
		if req.LeaseID == nil || !hostjerk.ValidLeaseID(*req.LeaseID) {
			return hostjerk.Call{}, protoError("unseal needs a lease_id")
		}
		return hostjerk.Call{Args: []string{p, "drop", "--lease", "--via-link"}, Env: []string{"FNOX_LEASE_ID=" + *req.LeaseID}}, nil
	}
	d := hostjerk.DefaultDuration
	if req.Duration != nil {
		if *req.Duration != float64(int(*req.Duration)) || *req.Duration < 1 || *req.Duration > hostjerk.MaxDuration {
			return hostjerk.Call{}, protoError("duration must be whole seconds from 1 to 86400")
		}
		d = int(*req.Duration)
	}
	return hostjerk.Call{Args: []string{p, "lease", "--via-link"}, Env: []string{"FNOX_LEASE_DURATION=" + strconv.Itoa(d)}}, nil
}

func printable(v string) bool {
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type event struct {
	Event   string `json:"event"`
	Message string `json:"message,omitempty"`
}

type leaseEvent struct {
	Event string `json:"event"`
	*hostjerk.Lease
}

type resultEvent struct {
	Event string `json:"event"`
	hostjerk.Result
}

func (s *Server) run(st *link.Stream, req open, call hostjerk.Call) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, st)
		select {
		case <-st.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	accepted := make(chan error, 1)
	call.Started = func() { accepted <- st.Accept(nil) }
	var writeMu sync.Mutex
	write := func(v any) {
		b, _ := json.Marshal(v)
		writeMu.Lock()
		_, _ = st.Write(append(b, '\n'))
		writeMu.Unlock()
	}
	if req.Op == "create" {
		call.Progress = func(m string) { write(event{Event: "progress", Message: m}) }
	}
	if call.Timeout <= 0 {
		call.Timeout = s.o.Timeout
	}
	p := ""
	if req.Provider != nil {
		p = *req.Provider
	}
	out := s.exec(ctx, req.Op, p, call)
	select {
	case err := <-accepted:
		if err != nil {
			return
		}
	default:
		_ = st.Reset("ECONNRESET: jerk couldn't start")
		return
	}
	s.o.Logf("jerk: %s %s: exit %d", req.Op, p, out.Result.Exit)
	switch {
	case ctx.Err() != nil:
		return
	case out.TimedOut && req.Op == "create":
		_ = st.Reset("ETIMEDOUT: the sign-in didn't finish in time")
		return
	case out.Died:
		_ = st.Reset("ECONNRESET: jerk died")
		return
	}
	if out.Lease != nil {
		write(leaseEvent{Event: "lease", Lease: out.Lease})
	}
	write(resultEvent{Event: "result", Result: out.Result})
	_ = st.CloseWrite()
}

func (s *Server) exec(ctx context.Context, op, p string, call hostjerk.Call) hostjerk.Outcome {
	j := s.o.Jerk
	switch op {
	case "seal":
		return j.LeaseCall(ctx, p, call)
	case "unseal":
		out := j.Run(ctx, p, call)
		out.Lease = nil
		return out
	}
	out := j.Run(ctx, p, call)
	out.Lease = nil
	if op == "list" {
		out.Result.Provider = ""
	}
	return out
}
