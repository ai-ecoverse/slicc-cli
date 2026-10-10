package flow

import (
	"encoding/json"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

const Schema = 1

type Kind string

const (
	Discovered Kind = "discovered"
	Launched   Kind = "launched"
	Attached   Kind = "attached"
	Serving    Kind = "serving"
	Connecting Kind = "connecting"
	Linked     Kind = "linked"
	LinkLost   Kind = "link-lost"
	StreamOpen Kind = "stream-open"
	StreamDone Kind = "stream-close"
	Status     Kind = "status"
	Kernel     Kind = "kernel"
	Closed     Kind = "closed"
	Error      Kind = "error"
)

var Public = map[Kind]bool{
	Discovered: true, Launched: true, Attached: true, Linked: true, LinkLost: true, Closed: true, Error: true,
}

const (
	RoleSeven = "seven"
	RoleExtra = "extra"
)

type Event struct {
	Schema       int             `json:"schema"`
	Event        Kind            `json:"event"`
	Entries      []browser.Entry `json:"entries,omitempty"`
	Browser      string          `json:"browser,omitempty"`
	Name         string          `json:"name,omitempty"`
	Version      string          `json:"version,omitempty"`
	Role         string          `json:"role,omitempty"`
	Launched     bool            `json:"launched,omitempty"`
	PID          int             `json:"pid,omitempty"`
	Profile      string          `json:"profile,omitempty"`
	ProfileDir   string          `json:"profileDir,omitempty"`
	CDP          string          `json:"cdp,omitempty"`
	WebSocketURL string          `json:"webSocketDebuggerUrl,omitempty"`
	Tab          string          `json:"tab,omitempty"`
	Peer         string          `json:"peer,omitempty"`
	Caps         []string        `json:"caps,omitempty"`
	Policy       string          `json:"policy,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	Message      string          `json:"message,omitempty"`
	Browsers     []string        `json:"browsers,omitempty"`
	Stream       string          `json:"stream,omitempty"`
	Kernel       string          `json:"kernel,omitempty"`
	Time         time.Time       `json:"-"`
}

func (e Event) MarshalJSON() ([]byte, error) {
	type plain Event
	p := plain(e)
	p.Schema = Schema
	switch e.Event {
	case Discovered:
		entries := p.Entries
		if entries == nil {
			entries = []browser.Entry{}
		}
		p.Entries = nil
		return json.Marshal(struct {
			plain
			Entries []browser.Entry `json:"entries"`
		}{p, entries})
	case Closed:
		closed := p.Browsers
		if closed == nil {
			closed = []string{}
		}
		p.Browsers = nil
		return json.Marshal(struct {
			plain
			Browsers []string `json:"browsers"`
		}{p, closed})
	}
	return json.Marshal(p)
}
