package jerk

import (
	"context"
	"errors"
	"sort"
	"time"

	hostjerk "github.com/ai-ecoverse/slicc-cli/internal/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

const (
	RevokeCap     = "jerk-revoke"
	RevokeKind    = "jerk-revoked"
	NoticeTimeout = 10 * time.Second
	SealRefused   = "EACCES: the host turned off sealing on this slicc"
	ByHost        = "host"
	BySeven       = "seven"
	OpSeal        = "seal"
	OpUnseal      = "unseal"
	OpRevoked     = "revoked"
)

type LeaseInfo struct {
	Provider  string    `json:"provider"`
	LeaseID   string    `json:"leaseId"`
	ExpiresAt string    `json:"expiresAt"`
	SealedAt  time.Time `json:"sealedAt"`
	expires   time.Time
}

type notice struct {
	provider string
	leaseID  string
	expires  time.Time
	sent     map[*link.Link]bool
}

type revokeOpen struct {
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	LeaseID  string `json:"lease_id"`
	By       string `json:"by"`
}

type UnsealResult struct {
	Code     string
	Exit     int
	Upstream *bool
}

type unsealState struct {
	gate     chan struct{}
	provider string
	users    int
	outcomes map[string]hostjerk.Outcome
	expires  time.Time
}

func (s *Server) lockUnseal(ctx context.Context, leaseID string) (*unsealState, func(), bool) {
	s.mu.Lock()
	s.pruneLocked()
	state := s.unsealing[leaseID]
	if state == nil {
		state = &unsealState{gate: make(chan struct{}, 1), outcomes: map[string]hostjerk.Outcome{}}
		state.gate <- struct{}{}
		if l := s.leases[leaseID]; l != nil {
			state.provider = l.Provider
			state.expires = l.expires
		}
		s.unsealing[leaseID] = state
	}
	state.users++
	s.mu.Unlock()
	releaseUser := func() {
		s.mu.Lock()
		state.users--
		if state.users == 0 && !state.expires.After(s.now()) {
			delete(s.unsealing, leaseID)
		}
		s.mu.Unlock()
	}
	waited := false
	select {
	case <-state.gate:
	default:
		waited = true
		select {
		case <-state.gate:
		case <-ctx.Done():
			releaseUser()
			return nil, nil, waited
		}
	}
	return state, func() {
		state.gate <- struct{}{}
		releaseUser()
	}, waited
}

func (s *Server) now() time.Time {
	if s.o.Now != nil {
		return s.o.Now()
	}
	return time.Now()
}

func (s *Server) notify(c node.Credential) {
	if s.o.Notify != nil {
		s.o.Notify(c)
	}
}

func (s *Server) record(provider string, l *hostjerk.Lease) {
	if l == nil || l.LeaseID == "" {
		return
	}
	exp, _ := time.Parse(time.RFC3339, l.ExpiresAt)
	s.mu.Lock()
	if state := s.unsealing[l.LeaseID]; state != nil && state.users == 0 {
		delete(s.unsealing, l.LeaseID)
	}
	s.leases[l.LeaseID] = &LeaseInfo{Provider: provider, LeaseID: l.LeaseID, ExpiresAt: l.ExpiresAt, SealedAt: s.now().UTC(), expires: exp}
	s.mu.Unlock()
	s.notify(node.Credential{Op: OpSeal, Provider: provider, LeaseID: l.LeaseID, ExpiresAt: l.ExpiresAt, By: BySeven})
}

func (s *Server) forget(provider, leaseID string, upstream *bool) {
	s.mu.Lock()
	expires, matched := "", false
	if l := s.leases[leaseID]; l != nil && l.Provider == provider {
		expires, matched = l.ExpiresAt, true
		delete(s.leases, leaseID)
	}
	if n := s.pending[leaseID]; n != nil && n.provider == provider {
		matched = true
		delete(s.pending, leaseID)
	}
	s.mu.Unlock()
	if !matched {
		return
	}
	s.notify(node.Credential{Op: OpUnseal, Provider: provider, LeaseID: leaseID, ExpiresAt: expires, By: BySeven, Upstream: upstream})
}

func (s *Server) pruneLocked() {
	now := s.now()
	for id, state := range s.unsealing {
		if state.users == 0 && !state.expires.After(now) {
			delete(s.unsealing, id)
		}
	}
	for id, l := range s.leases {
		if !l.expires.After(now) {
			delete(s.leases, id)
		}
	}
	for id, n := range s.pending {
		if !n.expires.After(now) {
			delete(s.pending, id)
		}
	}
}

func (s *Server) Leases() []LeaseInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	out := make([]LeaseInfo, 0, len(s.leases))
	for _, l := range s.leases {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].SealedAt.Equal(out[j].SealedAt) {
			return out[i].SealedAt.Before(out[j].SealedAt)
		}
		return out[i].LeaseID < out[j].LeaseID
	})
	return out
}

func (s *Server) Sealing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.sealOff
}

func (s *Server) SetSealing(on bool) {
	s.mu.Lock()
	s.sealOff = !on
	s.mu.Unlock()
	if s.n == nil {
		return
	}
	if on {
		s.n.Advertise(SealCap)
	} else {
		s.n.Withdraw(SealCap)
	}
}

func (s *Server) Unseal(ctx context.Context, leaseID string) UnsealResult {
	ctx, cancel := context.WithTimeout(ctx, min(s.o.Timeout, hostjerk.Timeout))
	defer cancel()
	if !hostjerk.ValidLeaseID(leaseID) || !Allowed(s.o.GOOS, leaseID) {
		return UnsealResult{Code: "EPROTO"}
	}
	state, release, waited := s.lockUnseal(ctx, leaseID)
	if state == nil {
		return UnsealResult{Code: "EBUSY"}
	}
	defer release()
	if waited {
		if out, ok := state.outcomes[state.provider]; ok {
			return UnsealResult{Upstream: out.Result.Revoked}
		}
	}
	if ctx.Err() != nil {
		return UnsealResult{Code: "EBUSY"}
	}
	s.mu.Lock()
	s.pruneLocked()
	l := s.leases[leaseID]
	if l == nil {
		s.mu.Unlock()
		return UnsealResult{Code: "ENOENT"}
	}
	s.mu.Unlock()
	call := hostjerk.Call{Args: []string{l.Provider, "drop", "--lease", "--via-link"}, Env: []string{"FNOX_LEASE_ID=" + leaseID}, Timeout: s.o.Timeout}
	out := s.o.Jerk.Run(ctx, l.Provider, call)
	s.o.Logf("jerk: unseal %s (host): exit %d", l.Provider, out.Result.Exit)
	if out.Died || out.TimedOut || out.Result.Exit != 0 {
		return UnsealResult{Code: "EIO", Exit: out.Result.Exit}
	}
	state.outcomes[l.Provider] = out
	s.mu.Lock()
	state.expires = l.expires
	delete(s.leases, leaseID)
	s.mu.Unlock()
	s.notify(node.Credential{Op: OpRevoked, Provider: l.Provider, LeaseID: leaseID, ExpiresAt: l.ExpiresAt, By: ByHost, Upstream: out.Result.Revoked})
	n := &notice{provider: l.Provider, leaseID: leaseID, expires: l.expires, sent: map[*link.Link]bool{}}
	s.mu.Lock()
	s.pending[leaseID] = n
	links := make([]*link.Link, 0, len(s.links))
	for k := range s.links {
		links = append(links, k)
	}
	s.mu.Unlock()
	for _, k := range links {
		go s.deliver(k)
	}
	return UnsealResult{Upstream: out.Result.Revoked}
}

func (s *Server) Attach(l *link.Link) {
	select {
	case <-l.Ready():
	case <-l.Done():
		return
	}
	select {
	case <-l.HelloSent():
	case <-l.Done():
		return
	}
	h, _ := l.PeerHello()
	if !h.HasCap(RevokeCap) {
		return
	}
	s.mu.Lock()
	s.links[l] = true
	s.mu.Unlock()
	go func() {
		<-l.Done()
		s.mu.Lock()
		delete(s.links, l)
		for _, n := range s.pending {
			delete(n.sent, l)
		}
		s.mu.Unlock()
	}()
	s.deliver(l)
}

func (s *Server) deliver(l *link.Link) {
	s.mu.Lock()
	s.pruneLocked()
	var due []*notice
	for _, n := range s.pending {
		if !n.sent[l] && s.links[l] {
			n.sent[l] = true
			due = append(due, n)
		}
	}
	s.mu.Unlock()
	sort.Slice(due, func(i, j int) bool { return due[i].leaseID < due[j].leaseID })
	for _, n := range due {
		s.send(l, n)
	}
}

func (s *Server) noticeTimeout() time.Duration {
	if s.o.NoticeTimeout <= 0 {
		return NoticeTimeout
	}
	return s.o.NoticeTimeout
}

func (s *Server) send(l *link.Link, n *notice) {
	ctx, cancel := context.WithTimeout(context.Background(), s.noticeTimeout())
	defer cancel()
	st, err := l.Open(ctx, revokeOpen{Kind: RevokeKind, Provider: n.provider, LeaseID: n.leaseID, By: ByHost}, link.OpenOptions{Shared: true})
	if errors.Is(err, context.DeadlineExceeded) {
		err = errors.New("ETIMEDOUT")
	}
	if err != nil {
		s.o.Logf("jerk: revoke notice %s: %v", n.provider, err)
		return
	}
	_ = st.CloseWrite()
	select {
	case <-st.Done():
	case <-ctx.Done():
		_ = st.Reset("ETIMEDOUT")
		s.o.Logf("jerk: revoke notice %s: ETIMEDOUT", n.provider)
	}
}
