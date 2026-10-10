package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var profileNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

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

func prepareProfile(dir string) error {
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
	markCleanExit(filepath.Join(def, "Preferences"))
	return nil
}

func markCleanExit(prefsPath string) {
	b, err := os.ReadFile(prefsPath)
	if err != nil {
		return
	}
	var prefs map[string]any
	if json.Unmarshal(b, &prefs) != nil {
		return
	}
	profile, _ := prefs["profile"].(map[string]any)
	if profile == nil {
		profile = map[string]any{}
		prefs["profile"] = profile
	}
	if profile["exit_type"] == "Normal" && profile["exited_cleanly"] == true {
		return
	}
	profile["exit_type"] = "Normal"
	profile["exited_cleanly"] = true
	if out, err := json.Marshal(prefs); err == nil {
		_ = os.WriteFile(prefsPath, out, 0o600)
	}
}
