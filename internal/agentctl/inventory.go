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

const EventInventory = "inventory"

var ErrNoInventory = errors.New("this seven doesn't report its agents; update it")

type UnavailableError struct{ Message string }

func (e *UnavailableError) Error() string { return e.Message }

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

type InventoryStream = SnapshotStream[Snapshot]

func parseSnapshot(b []byte) (Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("seven sent an inventory that isn't JSON: %w", err)
	}
	raw, err := rawObject(b, "an inventory")
	if err != nil {
		return s, err
	}
	s.Raw = raw
	return s, nil
}

func inventoryErr(err error) error { return snapshotErr(err, ErrNoInventory, "no inventory") }

func Inventory(ctx context.Context, l Link, watch bool) (*InventoryStream, error) {
	return openSnapshots(ctx, l, inventoryOpen{Kind: Kind, Op: "inventory", Watch: watch}, EventInventory, parseSnapshot, inventoryErr)
}

func InventoryLine(s Snapshot, schema int, received time.Time) []byte {
	return SnapshotLine(EventInventory, s.Raw, schema, received)
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
