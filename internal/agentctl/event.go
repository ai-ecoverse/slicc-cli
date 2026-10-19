package agentctl

import (
	"encoding/json"
	"unicode/utf8"
)

type Event struct {
	T         string `json:"t"`
	ID        string `json:"id,omitempty"`
	Text      string `json:"text,omitempty"`
	Delta     string `json:"delta,omitempty"`
	Delivered string `json:"delivered,omitempty"`
	Call      string `json:"call,omitempty"`
	Name      string `json:"name,omitempty"`
	Summary   string `json:"summary,omitempty"`
	OK        *bool  `json:"ok,omitempty"`
	State     string `json:"state,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Busy      bool   `json:"busy,omitempty"`

	Line []byte `json:"-"`
}

const (
	EventUser       = "user"
	EventText       = "text"
	EventThinking   = "thinking"
	EventTool       = "tool"
	EventToolResult = "tool-result"
	EventRun        = "run"
	EventNotice     = "notice"
	EventError      = "error"
	EventEnd        = "end"
	EventDelivered  = "delivered"
	EventBusy       = "busy"
)

const (
	EndStop    = "stop"
	EndAborted = "aborted"
	EndError   = "error"
)

func (e Event) streaming() bool { return e.T == EventText || e.T == EventThinking }

func (e Event) withDelta(delta string) Event {
	e.Delta = delta
	var m map[string]any
	if json.Unmarshal(e.Line, &m) == nil {
		m["delta"] = delta
		if b, err := json.Marshal(m); err == nil {
			e.Line = b
		}
	}
	return e
}

type Tracker struct {
	printed map[string]int
	recv    map[string]int
	seen    map[string]bool
	last    string
}

func NewTracker() *Tracker {
	return &Tracker{printed: map[string]int{}, recv: map[string]int{}, seen: map[string]bool{}}
}

func (t *Tracker) Since() string { return t.last }

func (t *Tracker) Reconnected() { t.recv = map[string]int{} }

func (t *Tracker) Filter(e Event) (Event, bool) {
	if e.ID == "" {
		return e, true
	}
	t.last = e.ID
	if !e.streaming() {
		if t.seen[e.ID] {
			return e, false
		}
		t.seen[e.ID] = true
		return e, true
	}
	start := t.recv[e.ID]
	end := start + len(e.Delta)
	t.recv[e.ID] = end
	done := t.printed[e.ID]
	if end <= done {
		return e, false
	}
	t.printed[e.ID] = end
	if start >= done {
		return e, true
	}
	cut := done - start
	for cut < len(e.Delta) && !utf8.RuneStart(e.Delta[cut]) {
		cut++
	}
	return e.withDelta(e.Delta[cut:]), true
}
