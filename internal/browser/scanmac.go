package browser

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type appMeta struct {
	bundleID string
	version  string
	exe      string
}

func discoverMac(ctx context.Context, o Options, p platform) []Installed {
	paths := macCandidatePaths(ctx, p)
	scan := !o.NoBundleScan
	var meta map[string]appMeta
	if scan {
		meta = plistMeta(paths)
	} else {
		meta = mdlsMeta(ctx, paths)
	}
	debug := macDebugBuilds(p)
	var out []Installed
	used := map[string]bool{}
	for _, path := range paths {
		in, ok := macInstalled(p, path, meta[path], scan)
		if d, isDebug := debug[path]; isDebug && (!ok || in.Engine == EngineElectron) {
			in, ok, used[path] = d, true, true
		}
		if ok {
			out = append(out, in)
		}
	}
	for _, path := range sortedKeys(debug) {
		if !used[path] {
			out = append(out, debug[path])
		}
	}
	return out
}

func macCandidatePaths(ctx context.Context, p platform) []string {
	var paths []string
	for _, dir := range p.macAppDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".app") && !strings.HasSuffix(e.Name(), " Debug.app") {
				paths = append(paths, filepath.Join(dir, e.Name()))
			}
		}
	}
	paths = append(paths, mdfindKnown(ctx)...)
	return uniqueStrings(paths)
}

func macBundleIDs() []string {
	var ids []string
	for _, k := range knownBrowsers {
		if k.mac.bundleID != "" {
			ids = append(ids, k.mac.bundleID)
		}
	}
	for _, a := range knownElectronApps {
		ids = append(ids, a.bundleID)
	}
	return ids
}

func mdfindQuery(ids []string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = `kMDItemCFBundleIdentifier == "` + id + `"`
	}
	return strings.Join(parts, " || ")
}

func mdfindKnown(ctx context.Context) []string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "mdfind", mdfindQuery(macBundleIDs())).Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if usableMdfindPath(line) {
			paths = append(paths, line)
		}
	}
	return paths
}

func usableMdfindPath(path string) bool {
	if !strings.HasSuffix(path, ".app") || !strings.HasPrefix(path, "/") {
		return false
	}
	if strings.Contains(strings.TrimSuffix(path, ".app"), ".app/") {
		return false
	}
	for _, bad := range []string{"/Library/", "/Volumes/", "/private/", "/.Trash/", "/System/"} {
		if strings.Contains(path, bad) {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]Installed) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func plistMeta(paths []string) map[string]appMeta {
	out := map[string]appMeta{}
	for _, path := range paths {
		m, err := readPlistStrings(filepath.Join(path, "Contents", "Info.plist"))
		if err != nil {
			continue
		}
		v := m["CFBundleShortVersionString"]
		if v == "" {
			v = m["CFBundleVersion"]
		}
		out[path] = appMeta{bundleID: m["CFBundleIdentifier"], version: v, exe: m["CFBundleExecutable"]}
	}
	return out
}

func mdlsMeta(ctx context.Context, paths []string) map[string]appMeta {
	out := map[string]appMeta{}
	if len(paths) == 0 {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := append([]string{"-raw", "-nullMarker", "", "-name", "kMDItemCFBundleIdentifier", "-name", "kMDItemVersion"}, paths...)
	b, err := exec.CommandContext(ctx, "mdls", args...).Output()
	if err != nil {
		return out
	}
	return parseMdls(b, paths)
}

func parseMdls(b []byte, paths []string) map[string]appMeta {
	out := map[string]appMeta{}
	fields := bytes.Split(b, []byte{0})
	for i, path := range paths {
		if 2*i+1 >= len(fields) {
			break
		}
		out[path] = appMeta{bundleID: string(fields[2*i]), version: string(fields[2*i+1])}
	}
	return out
}

func appName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".app")
}

func macExecutable(path, exe, fallback string) string {
	if exe == "" {
		exe = fallback
	}
	if exe == "" {
		exe = appName(path)
	}
	return filepath.Join(path, "Contents", "MacOS", exe)
}

func macKnownByAppName(path string) (known, bool) {
	base := filepath.Base(path)
	for _, k := range knownBrowsers {
		if k.mac.app == base {
			return k, true
		}
	}
	return known{}, false
}

func macInstalled(p platform, path string, m appMeta, scan bool) (Installed, bool) {
	k, ok := knownByBundleID(m.bundleID)
	if !ok && m.bundleID == "" {
		k, ok = macKnownByAppName(path)
	}
	if ok {
		in := Installed{
			ID: k.id, Name: k.name, Engine: EngineChromium, Channel: k.channel, Version: m.version,
			Path: path, Executable: macExecutable(path, m.exe, k.mac.exe), BundleID: k.mac.bundleID,
			DebugSupport: DebugSupported,
		}
		if dirs := p.userDataDirs(k); len(dirs) > 0 {
			in.UserDataDir = dirs[0]
		}
		return in, true
	}
	if !scan {
		a, ok := knownAppByBundleID(m.bundleID)
		if !ok {
			return Installed{}, false
		}
		return electronApp(a.name, path, macExecutable(path, "", ""), m.version, m.bundleID, DebugUnknown), true
	}
	if !hasCDPFramework(path) {
		return Installed{}, false
	}
	name := appName(path)
	return electronApp(name, path, macExecutable(path, m.exe, ""), m.version, m.bundleID, debugSupportFor(name)), true
}

func hasCDPFramework(path string) bool {
	fw := filepath.Join(path, "Contents", "Frameworks")
	return exists(filepath.Join(fw, "Electron Framework.framework")) || exists(filepath.Join(fw, "MSWebView2.framework"))
}

func macDebugBuilds(p platform) map[string]Installed {
	out := map[string]Installed{}
	dir := p.join(p.home, "Applications")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), " Debug.app") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if !hasCDPFramework(path) {
			continue
		}
		base := strings.TrimSuffix(e.Name(), " Debug.app")
		m := plistMeta([]string{path})[path]
		in := electronApp(base, path, macExecutable(path, m.exe, base), m.version, m.bundleID, DebugSupported)
		in.DebugBuild = true
		out[filepath.Join("/Applications", base+".app")] = in
	}
	return out
}

func electronApp(name, path, exe, version, bundleID string, ds DebugSupport) Installed {
	return Installed{
		ID: "electron:" + slug(name), Name: name, Engine: EngineElectron, Version: version,
		Path: path, Executable: exe, BundleID: bundleID, DebugSupport: ds,
	}
}

func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}
