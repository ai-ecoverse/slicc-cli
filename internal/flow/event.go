package flow

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

const Schema = 1

type Kind string

const (
	Discovered  Kind = "discovered"
	Launched    Kind = "launched"
	Attached    Kind = "attached"
	Serving     Kind = "serving"
	Connecting  Kind = "connecting"
	Linked      Kind = "linked"
	LinkLost    Kind = "link-lost"
	StreamOpen  Kind = "stream-open"
	StreamDone  Kind = "stream-close"
	Dropped     Kind = "dropped"
	Status      Kind = "status"
	Kernel      Kind = "kernel"
	BrowserGone Kind = "browser-gone"
	BrowserBack Kind = "browser-back"
	BrowserSlow Kind = "browser-slow"
	Closed      Kind = "closed"
	Error       Kind = "error"
	Pairing     Kind = "pairing"
	Mode        Kind = "mode"
	Credential  Kind = "credential"

	Reconnecting Kind = "reconnecting"

	Inventory Kind = "inventory"

	ForeignBrowser Kind = "foreign-browser"
)

var Public = map[Kind]bool{
	Discovered: true, Launched: true, Attached: true, Linked: true, LinkLost: true, BrowserGone: true, BrowserBack: true, BrowserSlow: true, Closed: true, Error: true, ForeignBrowser: true, Credential: true,
}

var Loopback = map[Kind]bool{Mode: true, Pairing: true}

var Lifecycle = map[Kind]bool{Connecting: true, Reconnecting: true}

var Agents = map[Kind]bool{Inventory: true}

func Names() []string {
	var out []string
	for _, set := range []map[Kind]bool{Public, Loopback, Lifecycle, Agents} {
		for k := range set {
			out = append(out, string(k))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

const (
	RoleSeven = "seven"
	RoleExtra = "extra"
)

const NoBrowserSession = "no-browser-session"

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
	Count        int             `json:"count,omitempty"`
	Kernel       string          `json:"kernel,omitempty"`
	Compute      string          `json:"compute,omitempty"`
	OpenedTab    *bool           `json:"openedTab,omitempty"`
	Elapsed      int             `json:"elapsed,omitempty"`
	URL          string          `json:"url,omitempty"`
	Mode         string          `json:"mode,omitempty"`
	Attempt      int             `json:"attempt,omitempty"`
	RetryAt      *time.Time      `json:"retryAt,omitempty"`
	Wait         int64           `json:"wait,omitempty"`
	Code         string          `json:"code,omitempty"`
	Retryable    *bool           `json:"retryable,omitempty"`
	Resources    *Resources      `json:"resources,omitempty"`
	Op           string          `json:"op,omitempty"`
	Provider     string          `json:"provider,omitempty"`
	LeaseID      string          `json:"leaseId,omitempty"`
	ExpiresAt    string          `json:"expiresAt,omitempty"`
	By           string          `json:"by,omitempty"`
	Upstream     *bool           `json:"upstream,omitempty"`
	Kept         []string        `json:"-"`
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
			Browsers []string  `json:"browsers"`
			Kept     *[]string `json:"kept,omitempty"`
		}{p, closed, keptList(e.Kept)})
	}
	return json.Marshal(p)
}

func keptList(kept []string) *[]string {
	if kept == nil {
		return nil
	}
	return &kept
}

func SlowLine(name string, elapsed int) string {
	return fmt.Sprintf("%s is still starting (slow disk?), %d s so far", name, elapsed)
}
