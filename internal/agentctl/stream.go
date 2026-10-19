package agentctl

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const (
	Cap  = "agent"
	Kind = "agent"

	MaxPrompt    = 16 << 20
	InlinePrompt = 60 << 10
	maxOpen      = 62 << 10
	maxLine      = 4 << 20
)

type WhenBusy string

const (
	FollowUp WhenBusy = "followUp"
	Steer    WhenBusy = "steer"
	Reject   WhenBusy = "reject"
)

var (
	ErrNotAllowed  = errors.New("this seven doesn't allow remote control from this device")
	ErrBusy        = errors.New("seven is busy")
	ErrTooLong     = fmt.Errorf("the prompt is longer than %d MiB", MaxPrompt>>20)
	ErrEmpty       = errors.New("the prompt is empty")
	ErrNotUTF8     = errors.New("the prompt is not UTF-8 text")
	ErrNoBusy      = errors.New("this seven doesn't report when its agent is idle; update it")
	errLineTooLong = errors.New("an event line is too long")
)

type PromptOptions struct {
	Text     string
	WhenBusy WhenBusy
	Thinking bool
}

type WatchOptions struct {
	Since    string
	Thinking bool
	Busy     bool
}

type promptOpen struct {
	Kind     string   `json:"kind"`
	Op       string   `json:"op"`
	Text     *string  `json:"text,omitempty"`
	Length   *int     `json:"length,omitempty"`
	WhenBusy WhenBusy `json:"whenBusy"`
	Thinking bool     `json:"thinking,omitempty"`
}

type watchOpen struct {
	Kind     string `json:"kind"`
	Op       string `json:"op"`
	Since    string `json:"since,omitempty"`
	Thinking bool   `json:"thinking,omitempty"`
	Busy     bool   `json:"busy,omitempty"`
}

type opOpen struct {
	Kind string `json:"kind"`
	Op   string `json:"op"`
}

type Stream struct {
	s         Pipe
	r         *bufio.Reader
	Delivered string
	Head      string
	Gap       bool
	Busy      *bool
}

func CheckPrompt(text string) error {
	switch {
	case text == "":
		return ErrEmpty
	case len(text) > MaxPrompt:
		return ErrTooLong
	case !utf8.ValidString(text):
		return ErrNotUTF8
	}
	return nil
}

func promptOpening(opts PromptOptions) (promptOpen, bool) {
	open := promptOpen{Kind: Kind, Op: "prompt", WhenBusy: opts.WhenBusy, Thinking: opts.Thinking}
	if len(opts.Text) <= InlinePrompt {
		open.Text = &opts.Text
		if b, err := json.Marshal(open); err == nil && len(b) <= maxOpen {
			return open, true
		}
		open.Text = nil
	}
	n := len(opts.Text)
	open.Length = &n
	return open, false
}

func Prompt(ctx context.Context, l Link, opts PromptOptions) (*Stream, error) {
	if err := CheckPrompt(opts.Text); err != nil {
		return nil, err
	}
	if !l.Allowed() {
		return nil, ErrNotAllowed
	}
	if opts.WhenBusy == "" {
		opts.WhenBusy = FollowUp
	}
	open, inline := promptOpening(opts)
	s, err := l.Open(ctx, open)
	if err != nil {
		return nil, mapErr(err)
	}
	st := newStream(s)
	var opened struct {
		Delivered string `json:"delivered"`
	}
	_ = json.Unmarshal(s.Opened(), &opened)
	st.Delivered = opened.Delivered
	if !inline {
		if _, err := io.WriteString(s, opts.Text); err != nil {
			_ = s.Reset("ECANCELED")
			return nil, mapErr(err)
		}
	}
	if err := s.CloseWrite(); err != nil {
		return nil, mapErr(err)
	}
	return st, nil
}

func Watch(ctx context.Context, l Link, opts WatchOptions) (*Stream, error) {
	if !l.Allowed() {
		return nil, ErrNotAllowed
	}
	s, err := l.Open(ctx, watchOpen{Kind: Kind, Op: "watch", Since: opts.Since, Thinking: opts.Thinking, Busy: opts.Busy})
	if err != nil {
		return nil, mapErr(err)
	}
	st := newStream(s)
	var opened struct {
		Head string `json:"head"`
		Gap  bool   `json:"gap"`
		Busy *bool  `json:"busy"`
	}
	_ = json.Unmarshal(s.Opened(), &opened)
	st.Head, st.Gap, st.Busy = opened.Head, opened.Gap, opened.Busy
	_ = s.CloseWrite()
	return st, nil
}

func Abort(ctx context.Context, l Link) (bool, error) {
	if !l.Allowed() {
		return false, ErrNotAllowed
	}
	s, err := l.Open(ctx, opOpen{Kind: Kind, Op: "abort"})
	if err != nil {
		return false, mapErr(err)
	}
	var opened struct {
		Aborted bool `json:"aborted"`
	}
	_ = json.Unmarshal(s.Opened(), &opened)
	_ = s.CloseWrite()
	_, _ = io.Copy(io.Discard, s)
	return opened.Aborted, nil
}

func newStream(s Pipe) *Stream {
	return &Stream{s: s, r: bufio.NewReaderSize(s, 64*1024)}
}

func (st *Stream) Next() (Event, error) {
	for {
		line, err := st.readLine()
		if err != nil {
			return Event{}, err
		}
		if len(line) == 0 {
			continue
		}
		var e Event
		if json.Unmarshal(line, &e) != nil || e.T == "" {
			continue
		}
		e.Line = line
		if e.T == "delivered" {
			st.Delivered = e.Delivered
		}
		return e, nil
	}
}

func (st *Stream) readLine() ([]byte, error) {
	var out []byte
	for {
		chunk, err := st.r.ReadSlice('\n')
		out = append(out, chunk...)
		if len(out) > maxLine {
			_ = st.s.Reset("EMSGSIZE")
			return nil, errLineTooLong
		}
		switch {
		case err == nil:
			return trimEOL(out), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(out) > 0:
			return trimEOL(out), nil
		default:
			return nil, mapErr(err)
		}
	}
}

func trimEOL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func (st *Stream) Close() error {
	return st.s.Reset("ECANCELED")
}

func (st *Stream) Done() <-chan struct{} { return st.s.Done() }

func mapErr(err error) error {
	code, msg, remote := remoteCode(err)
	if !remote {
		return err
	}
	switch code {
	case "EBUSY":
		return ErrBusy
	case "EACCES", "EOPNOTSUPP":
		if msg != "" {
			return &RefusedError{Message: msg}
		}
		return ErrNotAllowed
	}
	return err
}
