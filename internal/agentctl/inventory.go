package agentctl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

const EventInventory = "inventory"

var ErrNoInventory = errors.New("this seven doesn't report its agents; update it")

var Statuses = []string{"running", "queued", "waiting", "error", "idle"}

type InventoryAgent struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Parent   string `json:"parent"`
	Status   string `json:"status"`
	Queued   int    `json:"queued"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Since    int64  `json:"since"`
}

type Snapshot struct {
	Instance struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"instance"`
	Seq        int64            `json:"seq"`
	ChangedAt  int64            `json:"changedAt"`
	ReportedAt int64            `json:"reportedAt"`
	Counts     map[string]int   `json:"counts"`
	Agents     []InventoryAgent `json:"agents"`

	Raw map[string]json.RawMessage `json:"-"`
}

type inventoryOpen struct {
	Kind  string `json:"kind"`
	Op    string `json:"op"`
	Watch bool   `json:"watch,omitempty"`
}

type InventoryStream struct {
	s     Pipe
	r     *bufio.Reader
	First Snapshot
}

func parseSnapshot(b []byte) (Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("seven sent an inventory that isn't JSON: %w", err)
	}
	if err := json.Unmarshal(b, &s.Raw); err != nil || s.Raw == nil {
		return s, errors.New("seven sent an inventory that isn't a JSON object")
	}
	delete(s.Raw, "t")
	return s, nil
}

func inventoryErr(err error) error {
	code, msg, remote := remoteCode(err)
	if m, ok := linkMessage(err); ok && msg == "" {
		msg = m
	}
	switch {
	case !remote:
		return err
	case code == "EINVAL":
		return ErrNoInventory
	case code == "EOPNOTSUPP" && (msg == "" || strings.Contains(msg, "no inventory")):
		return ErrNoInventory
	case code == "EOPNOTSUPP":
		return errors.New(msg)
	}
	return mapErr(err)
}

func Inventory(ctx context.Context, l Link, watch bool) (*InventoryStream, error) {
	if !l.Allowed() {
		return nil, ErrNotAllowed
	}
	s, err := l.Open(ctx, inventoryOpen{Kind: Kind, Op: "inventory", Watch: watch})
	if err != nil {
		return nil, inventoryErr(err)
	}
	first, err := parseSnapshot(s.Opened())
	if err != nil {
		_ = s.Reset("EPROTO")
		return nil, err
	}
	_ = s.CloseWrite()
	return &InventoryStream{s: s, r: bufio.NewReaderSize(s, 64*1024), First: first}, nil
}

func (st *InventoryStream) Next() (Snapshot, error) {
	ev := &Stream{s: st.s, r: st.r}
	for {
		line, err := ev.readLine()
		if err != nil {
			return Snapshot{}, inventoryErr(err)
		}
		var head struct {
			T string `json:"t"`
		}
		if json.Unmarshal(line, &head) != nil || head.T != EventInventory {
			continue
		}
		if s, err := parseSnapshot(line); err == nil {
			return s, nil
		}
	}
}

func (st *InventoryStream) Close() error { return st.s.Reset("ECANCELED") }

func InventoryLine(s Snapshot, schema int, received time.Time) []byte {
	keys := make([]string, 0, len(s.Raw))
	for k := range s.Raw {
		switch k {
		case "schema", "event", "receivedAt":
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, `{"schema":%d,"event":%q`, schema, EventInventory)
	for _, k := range keys {
		var value bytes.Buffer
		if json.Compact(&value, s.Raw[k]) != nil {
			continue
		}
		name, _ := json.Marshal(k)
		fmt.Fprintf(&b, ",%s:%s", name, value.Bytes())
	}
	stamp, _ := json.Marshal(received.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, `,"receivedAt":%s}`, stamp)
	return []byte(b.String())
}

func shortID(id string) string {
	id = OneLineFull(id)
	if r := []rune(id); len(r) > 8 {
		return string(r[:8])
	}
	if id == "" {
		return "-"
	}
	return id
}

func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d/time.Second)))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

func modelOf(a InventoryAgent) string {
	provider, model := OneLineFull(a.Provider), OneLineFull(a.Model)
	switch {
	case provider == "":
		return "-"
	case model == "":
		return provider
	}
	return provider + "/" + model
}

func Summary(s Snapshot) string {
	var parts []string
	for _, status := range Statuses {
		if n := s.Counts[status]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, status))
		}
	}
	if len(parts) == 0 {
		return "no agents"
	}
	return strings.Join(parts, ", ")
}

func RenderInventory(w io.Writer, s Snapshot, now time.Time) {
	name := OneLineFull(s.Instance.Name)
	if name == "" {
		name = "seven"
	}
	reported := time.UnixMilli(s.ReportedAt)
	fmt.Fprintf(w, "%s: %s (reported %s ago)\n", name, Summary(s), Ago(now.Sub(reported)))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tPARENT\tSTATUS\tQUEUED\tMODEL\tSINCE")
	for _, a := range s.Agents {
		parent := "-"
		if a.Parent != "" {
			parent = shortID(a.Parent)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", shortID(a.ID), OneLineFull(a.Kind), parent, OneLineFull(a.Status), a.Queued, modelOf(a), Ago(reported.Sub(time.UnixMilli(a.Since))))
	}
	_ = tw.Flush()
}
