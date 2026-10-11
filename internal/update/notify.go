package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const NoCheckEnv = "SLICC_NO_UPDATE_CHECK"

type checkState struct {
	CheckedAt     time.Time `json:"checkedAt"`
	LatestVersion string    `json:"latestVersion"`
}

func DefaultStatePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "slicc", "update-check.json"), nil
}

type Notifier struct {
	Checker        *Checker
	StatePath      string
	Out            io.Writer
	Version        string
	Manager        Manager
	Now            func() time.Time
	CheckInterval  time.Duration
	RefreshWait    time.Duration
	RefreshTimeout time.Duration
}

func NewNotifier(version string, manager Manager, out io.Writer) *Notifier {
	if !IsReleaseVersion(version) || (manager != Unmanaged && manager != Go) || os.Getenv(NoCheckEnv) != "" {
		return nil
	}
	statePath, err := DefaultStatePath()
	if err != nil {
		return nil
	}
	return &Notifier{
		Checker:        NewChecker(),
		StatePath:      statePath,
		Out:            out,
		Version:        version,
		Manager:        manager,
		Now:            time.Now,
		CheckInterval:  24 * time.Hour,
		RefreshWait:    2 * time.Second,
		RefreshTimeout: 10 * time.Second,
	}
}

func (n *Notifier) readState() checkState {
	var state checkState
	if data, err := os.ReadFile(n.StatePath); err == nil {
		_ = json.Unmarshal(data, &state)
	}
	return state
}

func (n *Notifier) writeState(state checkState) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(n.StatePath), 0o755); err != nil {
		return
	}
	tmp := n.StatePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, n.StatePath)
}

func Notice(m Manager, latest, current string) string {
	return fmt.Sprintf("slicc %s is available (you have %s); run `%s`", Normalize(latest), Normalize(current), m.UpdateCommand())
}

func (n *Notifier) Start() func() {
	noop := func() {}
	if n == nil {
		return noop
	}
	state := n.readState()
	if n.Out != nil && state.LatestVersion != "" && IsNewer(state.LatestVersion, n.Version) {
		fmt.Fprintln(n.Out, Notice(n.Manager, state.LatestVersion, n.Version))
	}
	if n.Now().Sub(state.CheckedAt) < n.CheckInterval {
		return noop
	}
	n.writeState(checkState{CheckedAt: n.Now(), LatestVersion: state.LatestVersion})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), n.RefreshTimeout)
		defer cancel()
		if rel, err := n.Checker.Latest(ctx); err == nil {
			n.writeState(checkState{CheckedAt: n.Now(), LatestVersion: rel.Version})
		}
	}()
	return func() {
		timer := time.NewTimer(n.RefreshWait)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		}
	}
}
