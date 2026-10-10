package browser

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func discoverWindows(p platform, regPaths []string) []Installed {
	var out []Installed
	for _, path := range regPaths {
		if k, ok := classifyWindowsExe(path); ok && exists(path) {
			out = append(out, winInstalled(p, k, path))
		}
	}
	for _, k := range knownBrowsers {
		for _, path := range p.windowsInstallPaths(k) {
			if exists(path) {
				out = append(out, winInstalled(p, k, path))
			}
		}
	}
	return append(out, windowsElectronApps(p)...)
}

func winInstalled(p platform, k known, exe string) Installed {
	in := Installed{
		ID: k.id, Name: k.name, Engine: EngineChromium, Channel: k.channel, Path: exe, Executable: exe,
		Version: highestVersionDir(filepath.Dir(exe), ""), DebugSupport: DebugSupported,
	}
	if dirs := p.userDataDirs(k); len(dirs) > 0 {
		in.UserDataDir = dirs[0]
	}
	return in
}

var dottedVersion = regexp.MustCompile(`^\d+(\.\d+)+$`)

func highestVersionDir(dir, prefix string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var versions []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && strings.HasPrefix(name, prefix) && dottedVersion.MatchString(strings.TrimPrefix(name, prefix)) {
			versions = append(versions, strings.TrimPrefix(name, prefix))
		}
	}
	sort.Slice(versions, func(i, j int) bool { return compareVersions(versions[i], versions[j]) > 0 })
	if len(versions) == 0 {
		return ""
	}
	return versions[0]
}

func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func windowsElectronApps(p platform) []Installed {
	var out []Installed
	for _, root := range []string{p.join(p.winRoot(rootLAD), "Programs"), p.winRoot(rootPF), p.winRoot(rootPF86)} {
		for _, dir := range subdirs(root) {
			if isElectronDir(dir) {
				if exe := pickWindowsExe(dir, filepath.Base(dir)); exe != "" {
					out = append(out, electronApp(filepath.Base(dir), dir, exe, "", "", debugSupportFor(filepath.Base(dir))))
				}
			}
		}
	}
	for _, dir := range subdirs(p.winRoot(rootLAD)) {
		if !exists(filepath.Join(dir, "Update.exe")) {
			continue
		}
		v := highestVersionDir(dir, "app-")
		if v == "" {
			continue
		}
		appDir := filepath.Join(dir, "app-"+v)
		if !isElectronDir(appDir) {
			continue
		}
		if exe := pickWindowsExe(appDir, filepath.Base(dir)); exe != "" {
			name := strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe))
			out = append(out, electronApp(name, dir, exe, v, "", debugSupportFor(name)))
		}
	}
	return out
}

func subdirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out
}

func pickWindowsExe(dir, want string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var exes []string
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if e.IsDir() || !strings.HasSuffix(name, ".exe") || helperExe(name) {
			continue
		}
		if strings.TrimSuffix(name, ".exe") == strings.ToLower(want) {
			return filepath.Join(dir, e.Name())
		}
		exes = append(exes, filepath.Join(dir, e.Name()))
	}
	sort.Strings(exes)
	if len(exes) == 0 {
		return ""
	}
	return exes[0]
}

func helperExe(name string) bool {
	for _, s := range []string{"uninstall", "update", "elevate", "crashpad", "squirrel", "helper", "notification", "setup"} {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}
