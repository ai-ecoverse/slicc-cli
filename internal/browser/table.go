package browser

type macInfo struct {
	bundleID string
	app      string
	exe      string
	data     []string
}

type winInfo struct {
	roots     []string
	exe       string
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

var allRoots = []string{rootPF, rootPF86, rootLAD}

var knownBrowsers = []known{
	{
		id: "chrome", name: "Google Chrome", channel: "stable", chromium: true,
		mac: macInfo{bundleID: "com.google.Chrome", app: "Google Chrome.app", exe: "Google Chrome", data: []string{"Google", "Chrome"}},
		win: winInfo{roots: allRoots, exe: `Google\Chrome\Application\chrome.exe`, dataRoot: rootLAD, data: `Google\Chrome\User Data`, startMenu: []string{"Google Chrome"}},
		linux: linuxInfo{bins: []string{"google-chrome-stable", "google-chrome"}, desktop: []string{"google-chrome", "com.google.Chrome"},
			flatpak: "com.google.Chrome", config: "google-chrome", debs: []string{"google-chrome-stable"}},
	},
	{
		id: "edge", name: "Microsoft Edge", channel: "stable", chromium: true,
		mac: macInfo{bundleID: "com.microsoft.edgemac", app: "Microsoft Edge.app", exe: "Microsoft Edge", data: []string{"Microsoft Edge"}},
		win: winInfo{roots: allRoots, exe: `Microsoft\Edge\Application\msedge.exe`, dataRoot: rootLAD, data: `Microsoft\Edge\User Data`, startMenu: []string{"Microsoft Edge"}},
		linux: linuxInfo{bins: []string{"microsoft-edge-stable", "microsoft-edge"}, desktop: []string{"microsoft-edge", "com.microsoft.Edge"},
			flatpak: "com.microsoft.Edge", config: "microsoft-edge", debs: []string{"microsoft-edge-stable"}},
	},
	{
		id: "brave", name: "Brave Browser", channel: "stable",
		mac: macInfo{bundleID: "com.brave.Browser", app: "Brave Browser.app", exe: "Brave Browser", data: []string{"BraveSoftware", "Brave-Browser"}},
		win: winInfo{roots: allRoots, exe: `BraveSoftware\Brave-Browser\Application\brave.exe`, dataRoot: rootLAD, data: `BraveSoftware\Brave-Browser\User Data`, startMenu: []string{"Brave"}},
		linux: linuxInfo{bins: []string{"brave-browser", "brave-browser-stable", "brave"}, desktop: []string{"brave-browser", "com.brave.Browser", "brave_brave"},
			flatpak: "com.brave.Browser", snap: "brave", config: "BraveSoftware/Brave-Browser", debs: []string{"brave-browser"}},
	},
	{
		id: "opera", name: "Opera", channel: "stable",
		mac: macInfo{bundleID: "com.operasoftware.Opera", app: "Opera.app", exe: "Opera", data: []string{"com.operasoftware.Opera"}},
		win: winInfo{roots: []string{rootLAD, rootPF, rootPF86}, exe: `Programs\Opera\opera.exe|Opera\opera.exe`, dataRoot: rootAD, data: `Opera Software\Opera Stable`, startMenu: []string{"OperaStable"}},
		linux: linuxInfo{bins: []string{"opera"}, desktop: []string{"opera", "com.opera.Opera", "opera_opera"},
			flatpak: "com.opera.Opera", snap: "opera", config: "opera", debs: []string{"opera-stable"}},
	},
	{
		id: "vivaldi", name: "Vivaldi", channel: "stable",
		mac: macInfo{bundleID: "com.vivaldi.Vivaldi", app: "Vivaldi.app", exe: "Vivaldi", data: []string{"Vivaldi"}},
		win: winInfo{roots: []string{rootLAD, rootPF, rootPF86}, exe: `Vivaldi\Application\vivaldi.exe`, dataRoot: rootLAD, data: `Vivaldi\User Data`, startMenu: []string{"Vivaldi"}},
		linux: linuxInfo{bins: []string{"vivaldi-stable", "vivaldi"}, desktop: []string{"vivaldi-stable", "com.vivaldi.Vivaldi"},
			flatpak: "com.vivaldi.Vivaldi", config: "vivaldi", debs: []string{"vivaldi-stable"}},
	},
	{
		id: "arc", name: "Arc", channel: "stable",
		mac: macInfo{bundleID: "company.thebrowser.Browser", app: "Arc.app", exe: "Arc", data: []string{"Arc", "User Data"}},
	},
	{
		id: "atlas", name: "ChatGPT Atlas", channel: "stable",
		mac: macInfo{bundleID: "com.openai.atlas", app: "ChatGPT Atlas.app", exe: "ChatGPT Atlas"},
	},
	{
		id: "dia", name: "Dia", channel: "stable",
		mac: macInfo{bundleID: "company.thebrowser.dia", app: "Dia.app", exe: "Dia", data: []string{"Dia", "User Data"}},
	},
	{
		id: "chrome-beta", name: "Google Chrome Beta", channel: "beta", chromium: true,
		mac:   macInfo{bundleID: "com.google.Chrome.beta", app: "Google Chrome Beta.app", exe: "Google Chrome Beta", data: []string{"Google", "Chrome Beta"}},
		win:   winInfo{roots: allRoots, exe: `Google\Chrome Beta\Application\chrome.exe`, dataRoot: rootLAD, data: `Google\Chrome Beta\User Data`, startMenu: []string{"Google Chrome Beta"}},
		linux: linuxInfo{bins: []string{"google-chrome-beta"}, desktop: []string{"google-chrome-beta"}, config: "google-chrome-beta", debs: []string{"google-chrome-beta"}},
	},
	{
		id: "chrome-dev", name: "Google Chrome Dev", channel: "dev", chromium: true,
		mac: macInfo{bundleID: "com.google.Chrome.dev", app: "Google Chrome Dev.app", exe: "Google Chrome Dev", data: []string{"Google", "Chrome Dev"}},
		win: winInfo{roots: allRoots, exe: `Google\Chrome Dev\Application\chrome.exe`, dataRoot: rootLAD, data: `Google\Chrome Dev\User Data`, startMenu: []string{"Google Chrome Dev"}},
		linux: linuxInfo{bins: []string{"google-chrome-unstable"}, desktop: []string{"google-chrome-unstable", "com.google.ChromeDev"},
			flatpak: "com.google.ChromeDev", config: "google-chrome-unstable", debs: []string{"google-chrome-unstable"}},
	},
	{
		id: "chrome-canary", name: "Google Chrome Canary", channel: "canary", chromium: true,
		mac:   macInfo{bundleID: "com.google.Chrome.canary", app: "Google Chrome Canary.app", exe: "Google Chrome Canary", data: []string{"Google", "Chrome Canary"}},
		win:   winInfo{roots: []string{rootLAD}, exe: `Google\Chrome SxS\Application\chrome.exe`, dataRoot: rootLAD, data: `Google\Chrome SxS\User Data`, startMenu: []string{"Google Chrome Canary"}},
		linux: linuxInfo{bins: []string{"google-chrome-canary"}, desktop: []string{"google-chrome-canary"}, config: "google-chrome-canary", debs: []string{"google-chrome-canary"}},
	},
	{
		id: "brave-beta", name: "Brave Browser Beta", channel: "beta",
		mac:   macInfo{bundleID: "com.brave.Browser.beta", app: "Brave Browser Beta.app", exe: "Brave Browser Beta", data: []string{"BraveSoftware", "Brave-Browser-Beta"}},
		win:   winInfo{roots: allRoots, exe: `BraveSoftware\Brave-Browser-Beta\Application\brave.exe`, dataRoot: rootLAD, data: `BraveSoftware\Brave-Browser-Beta\User Data`},
		linux: linuxInfo{bins: []string{"brave-browser-beta"}, desktop: []string{"brave-browser-beta"}, config: "BraveSoftware/Brave-Browser-Beta", debs: []string{"brave-browser-beta"}},
	},
	{
		id: "brave-nightly", name: "Brave Browser Nightly", channel: "nightly",
		mac:   macInfo{bundleID: "com.brave.Browser.nightly", app: "Brave Browser Nightly.app", exe: "Brave Browser Nightly", data: []string{"BraveSoftware", "Brave-Browser-Nightly"}},
		win:   winInfo{roots: allRoots, exe: `BraveSoftware\Brave-Browser-Nightly\Application\brave.exe`, dataRoot: rootLAD, data: `BraveSoftware\Brave-Browser-Nightly\User Data`},
		linux: linuxInfo{bins: []string{"brave-browser-nightly"}, desktop: []string{"brave-browser-nightly"}, config: "BraveSoftware/Brave-Browser-Nightly", debs: []string{"brave-browser-nightly"}},
	},
	{
		id: "edge-beta", name: "Microsoft Edge Beta", channel: "beta", chromium: true,
		mac:   macInfo{bundleID: "com.microsoft.edgemac.Beta", app: "Microsoft Edge Beta.app", exe: "Microsoft Edge Beta", data: []string{"Microsoft Edge Beta"}},
		win:   winInfo{roots: allRoots, exe: `Microsoft\Edge Beta\Application\msedge.exe`, dataRoot: rootLAD, data: `Microsoft\Edge Beta\User Data`, startMenu: []string{"Microsoft Edge Beta"}},
		linux: linuxInfo{bins: []string{"microsoft-edge-beta"}, desktop: []string{"microsoft-edge-beta"}, config: "microsoft-edge-beta", debs: []string{"microsoft-edge-beta"}},
	},
	{
		id: "edge-dev", name: "Microsoft Edge Dev", channel: "dev", chromium: true,
		mac:   macInfo{bundleID: "com.microsoft.edgemac.Dev", app: "Microsoft Edge Dev.app", exe: "Microsoft Edge Dev", data: []string{"Microsoft Edge Dev"}},
		win:   winInfo{roots: allRoots, exe: `Microsoft\Edge Dev\Application\msedge.exe`, dataRoot: rootLAD, data: `Microsoft\Edge Dev\User Data`, startMenu: []string{"Microsoft Edge Dev"}},
		linux: linuxInfo{bins: []string{"microsoft-edge-dev"}, desktop: []string{"microsoft-edge-dev"}, config: "microsoft-edge-dev", debs: []string{"microsoft-edge-dev"}},
	},
	{
		id: "edge-canary", name: "Microsoft Edge Canary", channel: "canary", chromium: true,
		mac: macInfo{bundleID: "com.microsoft.edgemac.Canary", app: "Microsoft Edge Canary.app", exe: "Microsoft Edge Canary", data: []string{"Microsoft Edge Canary"}},
		win: winInfo{roots: []string{rootLAD}, exe: `Microsoft\Edge SxS\Application\msedge.exe`, dataRoot: rootLAD, data: `Microsoft\Edge SxS\User Data`, startMenu: []string{"Microsoft Edge Canary"}},
	},
	{
		id: "vivaldi-snapshot", name: "Vivaldi Snapshot", channel: "snapshot",
		mac:   macInfo{bundleID: "com.vivaldi.Vivaldi.snapshot", app: "Vivaldi Snapshot.app", exe: "Vivaldi Snapshot", data: []string{"Vivaldi Snapshot"}},
		linux: linuxInfo{bins: []string{"vivaldi-snapshot"}, desktop: []string{"vivaldi-snapshot"}, config: "vivaldi-snapshot", debs: []string{"vivaldi-snapshot"}},
	},
	{
		id: "chrome-for-testing", name: "Google Chrome for Testing", channel: "testing", chromium: true,
		mac:   macInfo{bundleID: "com.google.chrome.for.testing", app: "Google Chrome for Testing.app", exe: "Google Chrome for Testing", data: []string{"Google", "Chrome for Testing"}},
		win:   winInfo{dataRoot: rootLAD, data: `Google\Chrome for Testing\User Data`},
		linux: linuxInfo{config: "google-chrome-for-testing"},
	},
	{
		id: "chromium", name: "Chromium", channel: "stable", chromium: true,
		mac: macInfo{bundleID: "org.chromium.Chromium", app: "Chromium.app", exe: "Chromium", data: []string{"Chromium"}},
		win: winInfo{roots: []string{rootLAD, rootPF, rootPF86}, exe: `Chromium\Application\chrome.exe`, dataRoot: rootLAD, data: `Chromium\User Data`, startMenu: []string{"Chromium"}},
		linux: linuxInfo{bins: []string{"chromium", "chromium-browser"}, desktop: []string{"chromium", "chromium-browser", "chromium_chromium", "org.chromium.Chromium"},
			flatpak: "org.chromium.Chromium", snap: "chromium", config: "chromium", debs: []string{"chromium", "chromium-browser"}, snapDir: "chromium"},
	},
}

type knownApp struct {
	bundleID string
	name     string
}

var knownElectronApps = []knownApp{
	{"com.microsoft.teams2", "Microsoft Teams"},
	{"com.microsoft.VSCode", "Visual Studio Code"},
	{"com.microsoft.VSCodeInsiders", "VS Code Insiders"},
	{"com.spotify.client", "Spotify"},
	{"com.tinyspeck.slackmacgap", "Slack"},
	{"com.hnc.Discord", "Discord"},
	{"com.todesktop.230313mzl4w4u92", "Cursor"},
	{"com.figma.Desktop", "Figma"},
	{"notion.id", "Notion"},
	{"com.obsproject.obs-studio", "OBS Studio"},
	{"com.1password.1password", "1Password"},
	{"us.zoom.xos", "Zoom"},
	{"com.linear", "Linear"},
	{"com.loom.desktop", "Loom"},
	{"md.obsidian", "Obsidian"},
	{"com.bitwarden.desktop", "Bitwarden"},
	{"com.todoist.mac.Todoist", "Todoist"},
	{"com.github.GitHubClient", "GitHub Desktop"},
	{"com.postmanlabs.mac", "Postman"},
	{"org.whispersystems.signal-desktop", "Signal"},
	{"com.tdesktop.Telegram", "Telegram Desktop"},
	{"com.logseq.logseq", "Logseq"},
	{"com.whatsonchain.WhatsOnChain", "WhatsOnChain"},
	{"io.trystorybook.app", "Storybook"},
	{"com.mongodb.compass", "MongoDB Compass"},
	{"com.insomnia.app", "Insomnia"},
	{"com.jetbrains.toolbox", "JetBrains Toolbox"},
	{"com.electron.replit", "Replit"},
	{"com.twitch.studio", "Twitch Studio"},
}

var debugBlockedApps = []string{"Claude", "1Password"}

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
