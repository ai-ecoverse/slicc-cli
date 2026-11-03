package screen

import (
	"net"
	"sync"
)

const ProofEvent = "native-screen-verification"

type ProofChecks struct {
	TokenMatched *bool `json:"tokenMatched"`
	PIDAgreed    *bool `json:"pidAgreed"`
	UIDAgreed    *bool `json:"uidAgreed"`
}
type ProofReport struct {
	ProofChecks
	Schema  int    `json:"schema"`
	Event   string `json:"event"`
	Seq     int    `json:"seq"`
	Stage   string `json:"stage"`
	Lane    string `json:"lane,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	Lost    bool   `json:"lost"`
}
type ProofVerifier func(*net.UnixConn, Peer) (ProofChecks, error)
type ProofObserver struct {
	mu      sync.Mutex
	reports chan ProofReport
	seen    [4]bool
	pending [2]*ProofReport
	active  int
	seq     int
	closed  bool
	lost    bool
}
type proofConfig struct {
	observer *ProofObserver
	verify   ProofVerifier
}

func NewProofObserver() *ProofObserver               { return &ProofObserver{reports: make(chan ProofReport, 5)} }
func (p *ProofObserver) Reports() <-chan ProofReport { return p.reports }
func (p *ProofObserver) begin() func(string, ProofChecks, bool) {
	if p == nil {
		return func(string, ProofChecks, bool) {}
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return func(string, ProofChecks, bool) {}
	}
	p.active++
	p.mu.Unlock()
	return func(lane string, checks ProofChecks, accepted bool) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.active--
		p.recordLocked(lane, checks, accepted)
	}
}
func (p *ProofObserver) lose() {
	if p != nil {
		p.mu.Lock()
		p.lost = true
		p.mu.Unlock()
	}
}
func (p *ProofObserver) recordLocked(lane string, checks ProofChecks, accepted bool) {
	if p.closed {
		return
	}
	index := 0
	if lane == "frames" {
		index = 2
	}
	outcome := "refused"
	if accepted {
		index++
		outcome = "accepted"
	}
	if p.seen[index] {
		return
	}
	p.seen[index] = true
	r := ProofReport{ProofChecks: checks, Schema: 1, Event: ProofEvent, Stage: "check", Lane: lane, Outcome: outcome}
	if !accepted {
		p.pending[index/2] = &r
		return
	}
	p.emitLocked(r)
}
func (p *ProofObserver) emitLocked(r ProofReport) {
	p.seq++
	r.Seq = p.seq
	r.Lost = p.lost
	select {
	case p.reports <- r:
	default:
		p.lost = true
	}
}
func (p *ProofObserver) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	p.lost = p.lost || p.active > 0
	for _, r := range p.pending {
		if r != nil {
			p.emitLocked(*r)
		}
	}
	p.emitLocked(ProofReport{Schema: 1, Event: ProofEvent, Stage: "complete"})
	close(p.reports)
}
