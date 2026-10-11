package browser

import (
	"strings"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
)

type macInfo struct {
	bundleID string
	app      string
	exe      string
	data     []string
}

type winInfo struct {
	roots     []string
	exe       []string
	dataRoot  string
	data      string
	startMenu []string
}

type linuxInfo struct {
	bins    []string
	desktop []string
	flatpak string
	snap    string
	config  string
	debs    []string
	snapDir string
}

type known struct {
	id       string
	name     string
	channel  string
	chromium bool
	mac      macInfo
	win      winInfo
	linux    linuxInfo
}

const (
	rootPF   = "PROGRAMFILES"
	rootPF86 = "PROGRAMFILES(X86)"
	rootLAD  = "LOCALAPPDATA"
	rootAD   = "APPDATA"
)

type knownApp struct {
	bundleID string
	name     string
}

var catalog = sliccbrowsers.Default()

var knownBrowsers = knownFromCatalog(catalog)

var knownElectronApps = appsFromCatalog(catalog)

var debugBlockedApps = catalog.Electron.DebugBlocked

func knownFromCatalog(c *sliccbrowsers.Catalog) []known {
	out := make([]known, 0, len(c.Browsers))
	for _, b := range c.Browsers {
		k := known{id: b.ID, name: b.Name, channel: b.Channel, chromium: b.ChromiumVersion}
		k.mac = macInfo{bundleID: b.MacOS.BundleID, app: b.MacOS.App, exe: b.MacOS.Executable, data: b.MacOS.DataDir}
		w := b.Windows
		k.win = winInfo{roots: w.Roots, dataRoot: w.DataRoot, data: strings.Join(w.DataDir, `\`), startMenu: w.StartMenuInternet}
		for _, e := range w.Executables {
			k.win.exe = append(k.win.exe, strings.Join(e, `\`))
		}
		l := b.Linux
		k.linux = linuxInfo{bins: l.Bins, desktop: l.DesktopIDs, flatpak: l.Flatpak, snap: l.Snap, config: strings.Join(l.ConfigDir, "/"), debs: l.Debs, snapDir: strings.Join(l.SnapDataDir, "/")}
		out = append(out, k)
	}
	return out
}

func appsFromCatalog(c *sliccbrowsers.Catalog) []knownApp {
	out := make([]knownApp, 0, len(c.Electron.MacOSApps))
	for _, a := range c.Electron.MacOSApps {
		out = append(out, knownApp{bundleID: a.BundleID, name: a.Name})
	}
	return out
}

func knownByID(id string) (known, bool) {
	for _, k := range knownBrowsers {
		if k.id == id {
			return k, true
		}
	}
	return known{}, false
}

func knownByBundleID(bundleID string) (known, bool) {
	for _, k := range knownBrowsers {
		if k.mac.bundleID != "" && k.mac.bundleID == bundleID {
			return k, true
		}
	}
	return known{}, false
}

func knownAppByBundleID(bundleID string) (knownApp, bool) {
	for _, a := range knownElectronApps {
		if a.bundleID == bundleID {
			return a, true
		}
	}
	return knownApp{}, false
}

func priority(id string) int {
	for i, k := range knownBrowsers {
		if k.id == id {
			return i
		}
	}
	return len(knownBrowsers)
}

func debugSupportFor(name string) DebugSupport {
	for _, n := range debugBlockedApps {
		if n == name {
			return DebugDisabled
		}
	}
	return DebugSupported
}
