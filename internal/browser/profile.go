package browser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var profileNameRE = regexp.MustCompile(catalog.Profiles.NamePattern)

var ErrProfileInUse = errors.New("profile is in use")

func ValidProfileName(name string) bool {
	return profileNameRE.MatchString(name) && !strings.Contains(name, "..")
}

func DefaultProfileName(in Installed) string {
	return strings.ReplaceAll(in.ID, ":", "-")
}

func ProfileDir(o Options, in Installed, name string) (string, error) {
	if name == "" {
		name = DefaultProfileName(in)
	}
	if !ValidProfileName(name) {
		return "", fmt.Errorf("invalid profile name %q: use letters, digits, '.', '_' and '-'", name)
	}
	o = o.withDefaults()
	p := o.platform(runtime.GOOS)
	if in.Via == "snap" && o.ProfilesDir == "" {
		return filepath.Join(p.snapProfilesDir(in.Package), name), nil
	}
	return filepath.Join(o.profilesDir(p), name), nil
}

func lockPID(target string) int {
	i := strings.LastIndexByte(target, '-')
	if i < 0 {
		return 0
	}
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

func profileOwner(dir string) int {
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return 0
	}
	if pid := lockPID(target); pid > 0 && pidAlive(pid) {
		return pid
	}
	return 0
}

func prepareProfile(dir string, sites []string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if pid := profileOwner(dir); pid > 0 {
		return fmt.Errorf("%w: %s is held by process %d", ErrProfileInUse, dir, pid)
	}
	if err := os.Remove(filepath.Join(dir, "lockfile")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrProfileInUse, dir)
	}
	for _, name := range []string{ActivePortFile, "SingletonLock", "SingletonSocket", "SingletonCookie"} {
		_ = os.Remove(filepath.Join(dir, name))
	}
	def := filepath.Join(dir, "Default")
	_ = os.RemoveAll(filepath.Join(def, "Sessions"))
	_ = os.Remove(filepath.Join(def, "Last Session"))
	_ = os.Remove(filepath.Join(def, "Last Tabs"))
	_ = seedPreferences(filepath.Join(def, "Preferences"), sites, sites != nil)
	return nil
}

var SevenSitePattern = catalog.Seven.SitePattern

func SevenSites(extra []string) []string {
	sites := []string{SevenSitePattern}
	for _, o := range extra {
		if n := NormalizeOrigin(o); n != "" {
			if u, err := url.Parse(n); err == nil {
				sites = append(sites, u.Host)
			}
		}
	}
	return uniqueStrings(sites)
}

func SeedPreferences(prefsPath string, sites []string) error {
	return seedPreferences(prefsPath, sites, true)
}

func seedPreferences(prefsPath string, sites []string, seven bool) error {
	prefs := map[string]any{}
	existed := false
	b, err := os.ReadFile(prefsPath)
	switch {
	case err == nil:
		existed = true
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		if err := d.Decode(&prefs); err != nil || prefs == nil {
			return fmt.Errorf("%s: not a JSON object, left alone", prefsPath)
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	changed := false
	if existed {
		changed = markCleanExit(prefs)
	}
	if seven {
		changed = applySevenPrefs(prefs, sites, time.Now()) || changed
	}
	if !changed {
		return nil
	}
	out, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(prefsPath), 0o700); err != nil {
		return err
	}
	tmp := prefsPath + ".slicc-tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, prefsPath)
}

func setPref(m map[string]any, key string, v any, changed *bool) {
	if !reflect.DeepEqual(m[key], v) {
		m[key] = v
		*changed = true
	}
}

func markCleanExit(prefs map[string]any) bool {
	changed := false
	for _, p := range catalog.Seven.CleanExit {
		setPath(prefs, p.Path, prefValue(p.Value), &changed)
	}
	return changed
}

func applySevenPrefs(prefs map[string]any, sites []string, now time.Time) bool {
	changed := false
	for _, p := range catalog.Seven.Prefs {
		setPath(prefs, p.Path, prefValue(p.Value), &changed)
	}
	stamp := chromeTime(now)
	for _, d := range catalog.Seven.DiscardExceptions {
		parent := parentOf(prefs, d.Path, &changed)
		key := d.Path[len(d.Path)-1]
		switch d.Type {
		case "list":
			if mergeSiteList(parent, key, sites) {
				changed = true
			}
		case "chrome-time-map":
			withTime := child(parent, key, &changed)
			for _, site := range sites {
				if _, ok := withTime[site]; !ok {
					withTime[site] = stamp
					changed = true
				}
			}
		}
	}
	return changed
}

func prefValue(raw json.RawMessage) any {
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	_ = d.Decode(&v)
	return v
}

func parentOf(m map[string]any, path []string, changed *bool) map[string]any {
	for _, k := range path[:len(path)-1] {
		m = child(m, k, changed)
	}
	return m
}

func setPath(m map[string]any, path []string, v any, changed *bool) {
	setPref(parentOf(m, path, changed), path[len(path)-1], v, changed)
}

func mergeSiteList(discard map[string]any, key string, sites []string) bool {
	list, ok := discard[key].([]any)
	changed := !ok
	have := map[string]bool{}
	exceptions := []any{}
	for _, e := range list {
		if s, ok := e.(string); ok && !have[s] {
			have[s] = true
			exceptions = append(exceptions, s)
		}
	}
	changed = changed || len(exceptions) != len(list)
	for _, site := range sites {
		if !have[site] {
			have[site] = true
			exceptions = append(exceptions, site)
			changed = true
		}
	}
	discard[key] = exceptions
	return changed
}

func chromeTime(t time.Time) string {
	return strconv.FormatInt(t.UnixMicro()+11644473600000000, 10)
}

func child(m map[string]any, key string, changed *bool) map[string]any {
	if c, ok := m[key].(map[string]any); ok {
		return c
	}
	c := map[string]any{}
	m[key] = c
	*changed = true
	return c
}
