package flow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

var ErrNoBrowser = errors.New("no browser that can host seven was found; install Chrome or pass --cdp <url>")

func CanHost(e browser.Entry) error {
	if e.Kind == browser.KindEndpoint || e.Kind == browser.KindSix {
		return nil
	}
	if e.Installed == nil {
		return fmt.Errorf("%s is not a browser", e.ID)
	}
	if e.Installed.Engine == browser.EngineElectron {
		return fmt.Errorf("%s is an Electron app and can't host seven; select it with space as an extra browser", e.Name)
	}
	return nil
}

func CanExtra(e browser.Entry) error {
	if e.Kind == browser.KindInstalled && e.Installed != nil && e.Installed.DebugSupport == browser.DebugDisabled {
		return fmt.Errorf("%s blocks remote debugging", e.Name)
	}
	return nil
}

func IsSix(e browser.Entry) bool {
	return e.Six != nil || (e.Endpoint != nil && e.Endpoint.Six)
}

func Default(entries []browser.Entry) (browser.Entry, error) {
	for _, e := range entries {
		if !IsSix(e) && CanHost(e) == nil {
			return e, nil
		}
	}
	return browser.Entry{}, ErrNoBrowser
}

func Find(entries []browser.Entry, id string) (browser.Entry, bool) {
	for _, e := range entries {
		if e.ID == id {
			return e, true
		}
	}
	lower := strings.ToLower(id)
	for _, e := range entries {
		if strings.ToLower(e.ID) == lower || strings.ToLower(e.Name) == lower {
			return e, true
		}
	}
	return browser.Entry{}, false
}

func Resolve(entries []browser.Entry, ids []string) ([]browser.Entry, error) {
	if len(ids) == 0 {
		e, err := Default(entries)
		if err != nil {
			return nil, err
		}
		return []browser.Entry{e}, nil
	}
	var out []browser.Entry
	seen := map[string]bool{}
	for _, id := range ids {
		e, ok := Find(entries, id)
		if !ok {
			return nil, fmt.Errorf("no browser %q; see slicc browsers", id)
		}
		if IsSix(e) {
			return nil, sixRefused(e)
		}
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	return out, Check(out)
}

func Check(sel []browser.Entry) error {
	if len(sel) == 0 {
		return ErrNoBrowser
	}
	if err := CanHost(sel[0]); err != nil {
		return err
	}
	for _, e := range sel[1:] {
		if err := CanExtra(e); err != nil {
			return err
		}
	}
	return nil
}

func Profiles(o browser.Options, in browser.Installed) []string {
	dir, err := browser.ProfileDir(o, in, "")
	if err != nil {
		return nil
	}
	list, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range list {
		if d.IsDir() && browser.ValidProfileName(d.Name()) {
			out = append(out, d.Name())
		}
	}
	sort.Strings(out)
	return out
}
