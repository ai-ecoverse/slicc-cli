package browser

import (
	"os"
	"path"
	"strings"
)

type platform struct {
	goos string
	home string
	env  func(string) string
}

func (p platform) getenv(key string) string {
	if p.env == nil {
		return ""
	}
	return p.env(key)
}

func (p platform) join(parts ...string) string {
	if p.goos != "windows" {
		return path.Join(parts...)
	}
	var out []string
	for i, part := range parts {
		part = strings.ReplaceAll(part, "/", `\`)
		if i > 0 {
			part = strings.Trim(part, `\`)
		} else {
			part = strings.TrimRight(part, `\`)
		}
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, `\`)
}

func (p platform) winRoot(name string) string {
	if v := p.getenv(name); v != "" {
		return v
	}
	switch name {
	case rootPF:
		return `C:\Program Files`
	case rootPF86:
		return `C:\Program Files (x86)`
	case rootAD:
		return p.join(p.home, "AppData", "Roaming")
	default:
		return p.join(p.home, "AppData", "Local")
	}
}

func (p platform) xdg(name, fallback string) string {
	if v := p.getenv(name); v != "" && path.IsAbs(v) {
		return v
	}
	return p.join(p.home, fallback)
}

func (p platform) configDir() string {
	switch p.goos {
	case "darwin":
		return p.join(p.home, "Library", "Application Support")
	case "windows":
		return p.winRoot(rootAD)
	default:
		return p.xdg("XDG_CONFIG_HOME", ".config")
	}
}

const (
	ProfilesRoot       = "seven-profiles"
	legacyProfilesRoot = "profiles"
)

func (p platform) profilesDir() string {
	return p.join(p.configDir(), "slicc", ProfilesRoot)
}

func (p platform) snapProfilesDir(snap string) string {
	return p.join(p.home, "snap", snap, "common", "slicc", ProfilesRoot)
}

func (p platform) userDataDirs(k known) []string {
	var dirs []string
	switch p.goos {
	case "darwin":
		if len(k.mac.data) > 0 {
			dirs = append(dirs, p.join(append([]string{p.home, "Library", "Application Support"}, k.mac.data...)...))
		}
	case "windows":
		if k.win.data != "" {
			dirs = append(dirs, p.join(p.winRoot(k.win.dataRoot), k.win.data))
		}
	default:
		l := k.linux
		if l.config != "" {
			dirs = append(dirs, p.join(p.xdg("XDG_CONFIG_HOME", ".config"), l.config))
			if l.flatpak != "" {
				dirs = append(dirs, p.join(p.home, ".var", "app", l.flatpak, "config", l.config))
			}
		}
		if l.snap != "" && l.snapDir != "" {
			dirs = append(dirs, p.join(p.home, "snap", l.snap, "common", l.snapDir))
		}
	}
	return dirs
}

func (p platform) windowsInstallPaths(k known) []string {
	var out []string
	for _, root := range k.win.roots {
		for _, exe := range k.win.exes() {
			out = append(out, p.join(p.winRoot(root), exe))
		}
	}
	return out
}

func (w winInfo) exes() []string {
	if w.exe == "" {
		return nil
	}
	return strings.Split(w.exe, "|")
}

func classifyWindowsExe(exePath string) (known, bool) {
	lower := strings.ToLower(strings.ReplaceAll(exePath, "/", `\`))
	for _, k := range knownBrowsers {
		for _, exe := range k.win.exes() {
			if strings.HasSuffix(lower, `\`+strings.ToLower(exe)) {
				return k, true
			}
		}
	}
	return known{}, false
}

func (p platform) macAppDirs() []string {
	return []string{"/Applications", p.join(p.home, "Applications")}
}

func (p platform) linuxDesktopDirs() []string {
	dirs := []string{p.join(p.xdg("XDG_DATA_HOME", ".local/share"), "applications")}
	dataDirs := p.getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	for _, d := range strings.Split(dataDirs, ":") {
		if path.IsAbs(d) {
			dirs = append(dirs, p.join(d, "applications"))
		}
	}
	dirs = append(dirs,
		p.join(p.home, ".local", "share", "flatpak", "exports", "share", "applications"),
		"/var/lib/flatpak/exports/share/applications",
		"/var/lib/snapd/desktop/applications",
	)
	return uniqueStrings(dirs)
}

func (p platform) flatpakAppDirs(id string) []string {
	return []string{
		p.join(p.home, ".local", "share", "flatpak", "app", id),
		"/var/lib/flatpak/app/" + id,
	}
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
