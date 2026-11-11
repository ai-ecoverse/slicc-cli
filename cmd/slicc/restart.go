//go:build !wasip1

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
	"github.com/ai-ecoverse/slicc-cli/internal/telemetry"
	"github.com/ai-ecoverse/slicc-cli/internal/tui"
	"github.com/ai-ecoverse/slicc-cli/internal/update"
)

const resumeEnv = "SLICC_RESUME"

var resumed *resumeState

type resumeState struct {
	V        int             `json:"v"`
	Pair     bool            `json:"pair,omitempty"`
	Browsers []resumeBrowser `json:"browsers,omitempty"`
	Settings *tui.Settings   `json:"settings,omitempty"`
}

type resumeBrowser struct {
	CDP      string `json:"cdp"`
	Name     string `json:"name,omitempty"`
	Launched bool   `json:"launched,omitempty"`
}

func takeResume() *resumeState {
	raw, ok := os.LookupEnv(resumeEnv)
	if !ok {
		return nil
	}
	_ = os.Unsetenv(resumeEnv)
	var r resumeState
	if json.Unmarshal([]byte(raw), &r) != nil || r.V != 1 || (!r.Pair && len(r.Browsers) == 0) {
		return nil
	}
	return &r
}

func (r *resumeState) cdps() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.Browsers))
	for _, b := range r.Browsers {
		out = append(out, b.CDP)
	}
	return out
}

func (r *resumeState) adopt() map[string]bool {
	if r == nil {
		return nil
	}
	out := map[string]bool{}
	for _, b := range r.Browsers {
		if b.Launched {
			out[browser.EndpointKey(b.CDP)] = true
		}
	}
	return out
}

func (r *resumeState) choice(entries []browser.Entry) (tui.Choice, bool) {
	if r == nil {
		return tui.Choice{}, false
	}
	if r.Pair {
		for _, e := range entries {
			if flow.IsPair(e) {
				return tui.Choice{Entries: []browser.Entry{e}}, true
			}
		}
		return tui.Choice{}, false
	}
	var sel []browser.Entry
	for i, b := range r.Browsers {
		key := browser.EndpointKey(b.CDP)
		at := slices.IndexFunc(entries, func(e browser.Entry) bool {
			return e.Endpoint != nil && key != "" && browser.EndpointKey(e.Endpoint.CDP) == key
		})
		if at < 0 {
			if i == 0 {
				return tui.Choice{}, false
			}
			continue
		}
		e := entries[at]
		if b.Name != "" {
			e.Name = b.Name
		}
		sel = append(sel, e)
	}
	if flow.Check(sel) != nil {
		return tui.Choice{}, false
	}
	return tui.Choice{Entries: sel}, true
}

type seenBrowser struct {
	id       string
	name     string
	cdp      string
	seven    bool
	launched bool
	gone     bool
}

type session struct {
	mu       sync.Mutex
	pair     bool
	browsers []seenBrowser
	kept     []string
}

func (s *session) see(e flow.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch e.Event {
	case flow.Launched, flow.Attached:
		b := seenBrowser{id: e.Browser, name: e.Name, cdp: e.CDP, seven: e.Role == flow.RoleSeven, launched: e.Launched || e.Event == flow.Launched}
		if i := slices.IndexFunc(s.browsers, func(o seenBrowser) bool { return o.id == e.Browser }); i >= 0 {
			b.launched = b.launched || s.browsers[i].launched
			s.browsers[i] = b
		} else {
			s.browsers = append(s.browsers, b)
		}
	case flow.BrowserGone, flow.BrowserBack:
		for i := range s.browsers {
			if s.browsers[i].id == e.Browser {
				s.browsers[i].gone = e.Event == flow.BrowserGone && e.Reason != flow.NoBrowserSession
			}
		}
	case flow.Closed:
		s.kept = slices.Clone(e.Kept)
	}
}

func (s *session) resume(settings tui.Settings) resumeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := resumeState{V: 1, Pair: s.pair, Settings: &settings}
	if s.pair {
		return r
	}
	for _, seven := range []bool{true, false} {
		for _, b := range s.browsers {
			if b.seven == seven && !b.gone && b.cdp != "" {
				r.Browsers = append(r.Browsers, resumeBrowser{CDP: b.cdp, Name: b.name, Launched: b.launched})
			}
		}
	}
	return r
}

func (s *session) keptIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.kept)
}

type restartPlan struct {
	exe  string
	args []string
	env  []string
}

var restart *restartPlan

func planRestart(exe string, args []string, r resumeState) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, resumeEnv+"=") })
	restart = &restartPlan{exe: exe, args: slices.Clone(args), env: append(env, resumeEnv+"="+string(b))}
	return nil
}

func (p *restartPlan) failed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "slicc: restarting %s: %v; the browsers are still running, run slicc to link them again\n", p.exe, err)
	return 1
}

func updatableExe() (string, bool) {
	if !update.IsReleaseVersion(version) {
		return "", false
	}
	exe, err := update.Executable()
	if err != nil || update.ManagedBy(exe, launcher, goInstalled) != update.Unmanaged {
		return "", false
	}
	return exe, true
}

func dashboardUpdate(ctx context.Context, exe string, send func(tea.Msg)) {
	status := statusTo(func(e flow.Event) { send(tui.EventMsg(e)) })
	send(checkAndApply(ctx, exe, status))
}

func checkAndApply(ctx context.Context, exe string, status func(string, ...any)) tui.UpdateMsg {
	checker := update.NewChecker()
	rel, err := checker.Latest(ctx)
	if err != nil {
		telemetry.ReportError(telemetry.Update, err)
		return tui.UpdateMsg{Line: "update: " + err.Error(), Failed: true}
	}
	if !update.IsNewer(rel.Version, version) {
		return tui.UpdateMsg{Line: fmt.Sprintf("slicc %s is up to date", update.Normalize(version))}
	}
	status("updating slicc %s to %s", update.Normalize(version), rel.Version)
	if err := checker.Apply(ctx, rel, exe); err != nil {
		telemetry.ReportError(telemetry.Update, err)
		return tui.UpdateMsg{Line: "update: " + err.Error(), Failed: true}
	}
	return tui.UpdateMsg{Line: fmt.Sprintf("updated to slicc %s (SHA256SUMS and attestation checked); restarting", rel.Version), Restart: true}
}
