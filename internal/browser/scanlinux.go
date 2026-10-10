package browser

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type linuxFS struct {
	lookPath   func(string) (string, error)
	dpkgStatus string
}

func discoverLinux(ctx context.Context, p platform, fs linuxFS) []Installed {
	debs := parseDpkgStatus(fs.dpkgStatus)
	var out []Installed
	for _, k := range knownBrowsers {
		out = append(out, linuxKnown(ctx, p, fs, k, debs)...)
	}
	out = append(out, linuxDesktopEntries(p, fs)...)
	return out
}

func linuxKnown(ctx context.Context, p platform, fs linuxFS, k known, debs map[string]string) []Installed {
	l := k.linux
	var out []Installed
	base := Installed{ID: k.id, Name: k.name, Engine: EngineChromium, Channel: k.channel, DebugSupport: DebugSupported}
	if l.snap != "" {
		snapBin := "/snap/bin/" + l.snap
		if exists(snapBin) {
			in := base
			in.Path, in.Executable, in.Via, in.Package = snapBin, snapBin, "snap", l.snap
			in.Version = snapVersion("/snap/" + l.snap + "/current/meta/snap.yaml")
			in.UserDataDir = p.join(p.home, "snap", l.snap, "common", l.snapDir)
			out = append(out, in)
		}
	}
	for _, bin := range l.bins {
		path, err := fs.lookPath(bin)
		if err != nil {
			continue
		}
		in := base
		in.Path, in.Executable = path, path
		in.Version = debVersion(debs, l.debs)
		if in.Version == "" {
			in.Version = execVersion(ctx, path)
		}
		in.UserDataDir = p.join(p.xdg("XDG_CONFIG_HOME", ".config"), l.config)
		out = append(out, in)
	}
	if l.flatpak != "" {
		for _, dir := range p.flatpakAppDirs(l.flatpak) {
			if !isDir(dir) {
				continue
			}
			in := base
			in.Path, in.Via, in.Package = dir, "flatpak", l.flatpak
			if flatpak, err := fs.lookPath("flatpak"); err == nil {
				in.Executable = flatpak
			}
			in.UserDataDir = p.join(p.home, ".var", "app", l.flatpak, "config", l.config)
			out = append(out, in)
			break
		}
	}
	return out
}

func parseDpkgStatus(status string) map[string]string {
	out := map[string]string{}
	pkg, installed, version := "", false, ""
	flush := func() {
		if pkg != "" && installed && version != "" {
			out[pkg] = version
		}
		pkg, installed, version = "", false, ""
	}
	for _, line := range strings.Split(status, "\n") {
		switch {
		case strings.TrimSpace(line) == "":
			flush()
		case strings.HasPrefix(line, "Package: "):
			pkg = strings.TrimSpace(strings.TrimPrefix(line, "Package: "))
		case strings.HasPrefix(line, "Status: "):
			installed = strings.HasSuffix(strings.TrimSpace(line), " installed")
		case strings.HasPrefix(line, "Version: "):
			version = strings.TrimSpace(strings.TrimPrefix(line, "Version: "))
		}
	}
	flush()
	return out
}

var versionRE = regexp.MustCompile(`\d+(?:\.\d+)+`)

func debVersion(debs map[string]string, names []string) string {
	for _, n := range names {
		if v, ok := debs[n]; ok {
			if i := strings.IndexByte(v, ':'); i >= 0 {
				v = v[i+1:]
			}
			return versionRE.FindString(v)
		}
	}
	return ""
}

func snapVersion(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "version:") {
			return versionRE.FindString(line)
		}
	}
	return ""
}

func execVersion(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return ""
	}
	return versionRE.FindString(string(out))
}

type desktopEntry struct {
	id   string
	name string
	exec []string
}

func parseDesktopFile(id, contents string) (desktopEntry, bool) {
	d := desktopEntry{id: id}
	section := ""
	hidden := false
	sc := bufio.NewScanner(strings.NewReader(contents))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section != "[Desktop Entry]" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Name":
			d.name = strings.TrimSpace(val)
		case "Exec":
			d.exec = splitExec(strings.TrimSpace(val))
		case "Hidden", "NoDisplay":
			hidden = hidden || strings.TrimSpace(val) == "true"
		}
	}
	return d, len(d.exec) > 0 && !hidden
}

func splitExec(s string) []string {
	var out []string
	var cur strings.Builder
	quoted, have := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			quoted, have = !quoted, true
		case c == '\\' && quoted && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
		case (c == ' ' || c == '\t') && !quoted:
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	var args []string
	for _, a := range out {
		if !strings.HasPrefix(a, "%") {
			args = append(args, a)
		}
	}
	return args
}

func desktopProgram(args []string) string {
	i := 0
	if i < len(args) && filepath.Base(args[i]) == "env" {
		i++
		for i < len(args) && strings.Contains(args[i], "=") {
			i++
		}
	}
	if i < len(args) {
		return args[i]
	}
	return ""
}

func linuxDesktopEntries(p platform, fs linuxFS) []Installed {
	var out []Installed
	seen := map[string]bool{}
	for _, dir := range p.linuxDesktopDirs() {
		files, err := filepath.Glob(filepath.Join(dir, "*.desktop"))
		if err != nil {
			continue
		}
		for _, f := range files {
			id := strings.TrimSuffix(filepath.Base(f), ".desktop")
			if seen[id] {
				continue
			}
			seen[id] = true
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			d, ok := parseDesktopFile(id, string(b))
			if !ok {
				continue
			}
			if in, ok := desktopInstalled(p, fs, d); ok {
				out = append(out, in)
			}
		}
	}
	return out
}

func desktopInstalled(p platform, fs linuxFS, d desktopEntry) (Installed, bool) {
	prog := desktopProgram(d.exec)
	if prog == "" || filepath.Base(prog) == "flatpak" {
		return Installed{}, false
	}
	exe := prog
	if !filepath.IsAbs(exe) {
		lp, err := fs.lookPath(exe)
		if err != nil {
			return Installed{}, false
		}
		exe = lp
	}
	if !exists(exe) {
		return Installed{}, false
	}
	for _, k := range knownBrowsers {
		for _, id := range k.linux.desktop {
			if id == d.id {
				return Installed{
					ID: k.id, Name: k.name, Engine: EngineChromium, Channel: k.channel, Path: exe, Executable: exe,
					UserDataDir: p.join(p.xdg("XDG_CONFIG_HOME", ".config"), k.linux.config), DebugSupport: DebugSupported,
				}, true
			}
		}
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil || !isElectronDir(filepath.Dir(real)) {
		return Installed{}, false
	}
	name := d.name
	if name == "" {
		name = d.id
	}
	return electronApp(name, exe, real, "", "", debugSupportFor(name)), true
}

func isElectronDir(dir string) bool {
	if !exists(filepath.Join(dir, "resources", "app.asar")) && !isDir(filepath.Join(dir, "resources", "app")) {
		return false
	}
	for _, f := range []string{"v8_context_snapshot.bin", "chrome_100_percent.pak", "resources.pak", "icudtl.dat"} {
		if exists(filepath.Join(dir, f)) {
			return true
		}
	}
	return false
}
