package flow

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

var errSixNoBrowser = errors.New("no Chrome to open SLICC six's profile with; install Chrome or name one with --browser")

var errSixFailed = errors.New("SLICC six's profile: the launch or attach failed")

func installedOf(entries []browser.Entry) []browser.Installed {
	var out []browser.Installed
	for _, e := range entries {
		if e.Kind == browser.KindInstalled && e.Installed != nil {
			out = append(out, *e.Installed)
		}
	}
	return out
}

func sixEntry(six browser.Six, entries []browser.Entry, id string) (browser.Entry, error) {
	if id == "" {
		in, ok := browser.SixBrowser(six.Dir, installedOf(entries))
		if !ok {
			return browser.SixProfileEntry(six, nil), nil
		}
		return browser.SixProfileEntry(six, &in), nil
	}
	e, ok := Find(entries, id)
	if !ok {
		return browser.Entry{}, fmt.Errorf("no browser %q; see slicc browsers", id)
	}
	if e.Kind != browser.KindInstalled || e.Installed.Engine != browser.EngineChromium {
		return browser.Entry{}, fmt.Errorf("--browser %s: with --profile six, --browser names the installed Chromium browser that opens six's profile", id)
	}
	in := *e.Installed
	return browser.SixProfileEntry(six, &in), nil
}

func ResolveSix(o browser.Options, port int, entries []browser.Entry, ids []string) ([]browser.Entry, error) {
	six := browser.SixProfileFor(o, port)
	if err := browser.SixExists(six.Dir); err != nil {
		return nil, err
	}
	first := ""
	if len(ids) > 0 {
		first = ids[0]
	}
	host, err := sixEntry(six, entries, first)
	if err != nil {
		return nil, err
	}
	sel := []browser.Entry{host}
	seen := map[string]bool{host.ID: true}
	for _, id := range ids[min(1, len(ids)):] {
		e, ok := Find(entries, id)
		if !ok {
			return nil, fmt.Errorf("no browser %q; see slicc browsers", id)
		}
		if IsSix(e) {
			return nil, sixRefused(e)
		}
		if !seen[e.ID] {
			seen[e.ID] = true
			sel = append(sel, e)
		}
	}
	return sel, Check(sel)
}

func sixRefused(e browser.Entry) error {
	return fmt.Errorf("%s is SLICC six's Chrome; only --profile six uses it", e.ID)
}

func WithSixRows(o browser.Options, entries []browser.Entry) []browser.Entry {
	profiles := browser.FindSixProfiles(o)
	if len(profiles) == 0 {
		return entries
	}
	var out []browser.Entry
	for _, e := range entries {
		if e.Endpoint != nil && e.Endpoint.Six && coveredBy(e.Endpoint.UserDataDir, profiles) {
			continue
		}
		out = append(out, e)
	}
	for _, six := range profiles {
		row, _ := sixEntry(six, entries, "")
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tier < out[j].Tier })
	return out
}

func coveredBy(dir string, profiles []browser.Six) bool {
	for _, p := range profiles {
		if dir != "" && browser.SameDir(dir, p.Dir) {
			return true
		}
	}
	return false
}

func SixLabel(e browser.Entry) string {
	if e.Six != nil {
		return e.Six.Label
	}
	return browser.SixProfile
}

func (r *run) resolveSix(ctx context.Context, e browser.Entry) (browser.Entry, error) {
	if e.Six == nil || e.Kind != browser.KindSix {
		return e, nil
	}
	ep, err := browser.ProbeSix(ctx, r.cfg.Browser, e.Six.Dir)
	if err != nil {
		return e, err
	}
	if ep != nil {
		at := browser.SixEntry(*ep)
		at.Six = e.Six
		return at, nil
	}
	if e.Installed == nil {
		return e, errSixNoBrowser
	}
	e.Kind = browser.KindInstalled
	return e, nil
}

func reportable(e browser.Entry, err error) error {
	if !IsSix(e) {
		return err
	}
	for _, known := range []error{browser.ErrSixBusy, browser.ErrSixMissing, errSixNoBrowser} {
		if errors.Is(err, known) {
			return known
		}
	}
	return errSixFailed
}
