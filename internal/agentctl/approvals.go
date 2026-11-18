package agentctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	EventApprovals = "approvals"
	DecidedEvent   = "decided"
)

var (
	ErrNoApprovals = errors.New("this seven doesn't report its approvals; update it")
	ErrNoDecide    = errors.New("this seven doesn't take answers to its approvals; update it")
)

var Answers = []string{"once", "always", "deny", "stop"}

type ApprovalItem struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	AgentID   string `json:"agentId"`
	AgentName string `json:"agentName"`
	Requester struct {
		Kind  string `json:"kind"`
		Name  string `json:"name"`
		Where string `json:"where"`
	} `json:"requester"`
	Detail    json.RawMessage `json:"detail"`
	Tier      string          `json:"tier"`
	Always    bool            `json:"always"`
	Stoppable bool            `json:"stoppable"`
	Verdict   *struct {
		Level int `json:"level"`
	} `json:"verdict"`
	Judging      bool   `json:"judging"`
	VerdictError string `json:"verdictError"`
	CreatedAt    int64  `json:"createdAt"`
	ExpiresAt    int64  `json:"expiresAt"`
}

type Approvals struct {
	Approvals []ApprovalItem `json:"approvals"`

	Raw map[string]json.RawMessage `json:"-"`
}

type ApprovalsStream = SnapshotStream[Approvals]

type approvalsOpen struct {
	Kind  string `json:"kind"`
	Op    string `json:"op"`
	Watch bool   `json:"watch,omitempty"`
}

type decideOpen struct {
	Kind   string `json:"kind"`
	Op     string `json:"op"`
	ID     string `json:"id"`
	Answer string `json:"answer"`
	Reason string `json:"reason,omitempty"`
}

type DecideRefusal struct {
	Code    string
	Message string
}

func (e *DecideRefusal) Error() string { return e.Message }

func parseApprovals(b []byte) (Approvals, error) {
	var a Approvals
	if err := json.Unmarshal(b, &a); err != nil {
		return a, fmt.Errorf("seven sent approvals that aren't JSON: %w", err)
	}
	raw, err := rawObject(b, "approvals")
	if err != nil {
		return a, err
	}
	if _, ok := raw["approvals"]; !ok {
		raw["approvals"] = json.RawMessage("[]")
	}
	a.Raw = raw
	return a, nil
}

func approvalsErr(err error) error { return snapshotErr(err, ErrNoApprovals, "no approvals") }

func ListApprovals(ctx context.Context, l Link, watch bool) (*ApprovalsStream, error) {
	return openSnapshots(ctx, l, approvalsOpen{Kind: Kind, Op: "approvals", Watch: watch}, EventApprovals, parseApprovals, approvalsErr)
}

func ApprovalsLine(a Approvals, schema int, received time.Time) []byte {
	return SnapshotLine(EventApprovals, a.Raw, schema, received)
}

func Decide(ctx context.Context, l Link, id, answer, reason string) error {
	if !l.Allowed() {
		return ErrNotAllowed
	}
	s, err := l.Open(ctx, decideOpen{Kind: Kind, Op: "decide", ID: id, Answer: answer, Reason: reason})
	if err != nil {
		return decideErr(err)
	}
	_ = s.CloseWrite()
	_, _ = io.Copy(io.Discard, s)
	return nil
}

func decideErr(err error) error {
	code, msg, remote := remoteCode(err)
	if m, ok := linkMessage(err); ok && msg == "" {
		msg = m
	}
	switch {
	case !remote:
		return err
	case code == "EACCES" && msg != "":
		return &RefusedError{Message: msg}
	case code == "EACCES":
		return ErrNotAllowed
	case code == "EINVAL" && msg == "":
		return ErrNoDecide
	case code == "EINVAL":
		return &DecideRefusal{Code: code, Message: msg}
	case code == "ENOENT":
		if msg == "" {
			msg = "seven has no approval with that id"
		}
		return &DecideRefusal{Code: code, Message: msg}
	case code == "EOPNOTSUPP" && (msg == "" || strings.Contains(msg, "no approvals")):
		return ErrNoDecide
	case code == "EOPNOTSUPP":
		return &UnavailableError{Message: msg}
	}
	return err
}

func approvalDetail(a ApprovalItem) string {
	var s string
	if json.Unmarshal(a.Detail, &s) == nil {
		return s
	}
	var d struct {
		Text        string            `json:"text"`
		Attachments []json.RawMessage `json:"attachments"`
		Tool        string            `json:"tool"`
		Summary     string            `json:"summary"`
		Command     string            `json:"command"`
		What        string            `json:"what"`
		Mode        string            `json:"mode"`
		Seconds     float64           `json:"seconds"`
		Program     string            `json:"program"`
	}
	_ = json.Unmarshal(a.Detail, &d)
	switch {
	case d.Command != "":
		return d.Command
	case d.Tool != "" && d.Summary != "":
		return d.Tool + ": " + d.Summary
	case d.Tool != "" || d.Summary != "":
		return d.Tool + d.Summary
	case d.What != "":
		return captureDetail(d.What, d.Mode, d.Seconds, d.Program)
	case len(d.Attachments) > 0:
		return fmt.Sprintf("%s (+%d attached)", d.Text, len(d.Attachments))
	}
	return d.Text
}

func captureDetail(what, mode string, seconds float64, program string) string {
	out := strings.TrimSpace(what + " " + mode)
	if seconds > 0 {
		out += fmt.Sprintf(" %gs", seconds)
	}
	if program != "" {
		out += " for " + program
	}
	return out
}

func approvalAgent(a ApprovalItem) string {
	if name := OneLineFull(a.AgentName); name != "" {
		return name
	}
	if a.AgentID != "" {
		return shortID(a.AgentID)
	}
	if name := OneLineFull(a.Requester.Name); name != "" {
		return name
	}
	return "-"
}

func approvalVerdict(a ApprovalItem) string {
	switch {
	case a.Verdict != nil && a.Verdict.Level > 0:
		return fmt.Sprintf("level %d", a.Verdict.Level)
	case a.Judging:
		return "judging"
	case a.VerdictError != "":
		return "no verdict"
	}
	return "-"
}

func approvalLeft(a ApprovalItem, now time.Time) string {
	left := time.UnixMilli(a.ExpiresAt).Sub(now)
	if a.ExpiresAt == 0 || left <= 0 {
		return "expired"
	}
	return Ago(left) + " left"
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func RenderApprovals(w io.Writer, a Approvals, now time.Time) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, it := range a.Approvals {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", or(OneLineFull(it.ID), "-"), or(OneLineFull(it.Kind), "-"), approvalAgent(it), approvalVerdict(it), approvalLeft(it, now), or(OneLine(approvalDetail(it)), "-"))
	}
	_ = tw.Flush()
}
