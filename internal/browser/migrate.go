package browser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var sixProfileRE = regexp.MustCompile(`^browser-coding-agent-chrome(-[0-9]+)?$`)

func MigrateProfiles(o Options) []string {
	if o.ProfilesDir != "" {
		return nil
	}
	o = o.withDefaults()
	p := platform{goos: runtime.GOOS, home: o.Home, env: o.Getenv}
	pairs := [][2]string{{p.join(p.configDir(), "slicc", legacyProfilesRoot), p.profilesDir()}}
	if p.goos == "linux" {
		for _, k := range knownBrowsers {
			if k.linux.snap != "" {
				base := p.join(p.home, "snap", k.linux.snap, "common", "slicc")
				pairs = append(pairs, [2]string{p.join(base, legacyProfilesRoot), p.snapProfilesDir(k.linux.snap)})
			}
		}
	}
	var notices []string
	seen := map[string]bool{}
	for _, pair := range pairs {
		if seen[pair[1]] {
			continue
		}
		seen[pair[1]] = true
		notices = append(notices, migrateProfiles(pair[0], pair[1])...)
	}
	return notices
}

func migrateProfiles(from, to string) []string {
	if _, err := os.Lstat(to); !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		return nil
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return []string{fmt.Sprintf("can't create %s (%v); SLICC profiles stay in %s", to, err, from)}
	}
	var notices []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || sixProfileRE.MatchString(name) || !ValidProfileName(name) {
			continue
		}
		src, dst := filepath.Join(from, name), filepath.Join(to, name)
		if pid := profileOwner(src); pid > 0 {
			notices = append(notices, fmt.Sprintf("profile %s is in use by process %d, so it stays in %s; quit that browser, then move it to %s", name, pid, from, to))
			continue
		}
		if err := os.Rename(src, dst); err != nil && !errors.Is(err, os.ErrNotExist) {
			notices = append(notices, fmt.Sprintf("profile %s stays in %s: %v", name, from, err))
		}
	}
	return notices
}
