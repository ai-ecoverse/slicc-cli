package browser

import (
	"context"
	"net"
	"net/http"
	"slices"
	"time"
)

var MinChromiumMajor = catalog.MinChromiumMajor

const SchemaVersion = 1

var DefaultProbePorts = slices.Clone(catalog.Discovery.ProbePorts)

type Engine string

const (
	EngineChromium Engine = "chromium"
	EngineElectron Engine = "electron"
)

type DebugSupport string

const (
	DebugSupported DebugSupport = "supported"
	DebugDisabled  DebugSupport = "disabled"
	DebugUnknown   DebugSupport = "unknown"
)

type Source string

const (
	SourceActivePort Source = "devtools-active-port"
	SourceProbe      Source = "probe"
	SourceFlag       Source = "flag"
	SourceProcess    Source = "process"
)

type Installed struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Engine       Engine       `json:"engine"`
	Channel      string       `json:"channel,omitempty"`
	Version      string       `json:"version,omitempty"`
	Path         string       `json:"path"`
	Executable   string       `json:"executable"`
	BundleID     string       `json:"bundleId,omitempty"`
	Via          string       `json:"via,omitempty"`
	Package      string       `json:"package,omitempty"`
	UserDataDir  string       `json:"userDataDir,omitempty"`
	DebugSupport DebugSupport `json:"debugSupport"`
	DebugBuild   bool         `json:"debugBuild,omitempty"`
}

type Tab struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

type Endpoint struct {
	CDP          string `json:"cdp"`
	WebSocketURL string `json:"webSocketDebuggerUrl"`
	Name         string `json:"name"`
	Version      string `json:"version,omitempty"`
	Product      string `json:"product,omitempty"`
	UserAgent    string `json:"userAgent,omitempty"`
	Source       Source `json:"source"`
	Browser      string `json:"browser,omitempty"`
	UserDataDir  string `json:"userDataDir,omitempty"`
	Profile      string `json:"profile,omitempty"`
	WSOnly       bool   `json:"wsOnly,omitempty"`
	SevenOpen    bool   `json:"sevenOpen"`
	SevenTabs    []Tab  `json:"sevenTabs"`
}

type Kind string

const (
	KindEndpoint  Kind = "endpoint"
	KindInstalled Kind = "installed"
)

type Entry struct {
	ID        string     `json:"id"`
	Kind      Kind       `json:"kind"`
	Tier      int        `json:"tier"`
	Name      string     `json:"name"`
	Version   string     `json:"version,omitempty"`
	Endpoint  *Endpoint  `json:"endpoint,omitempty"`
	Installed *Installed `json:"installed,omitempty"`
}

type Options struct {
	Home         string
	Getenv       func(string) string
	ProbePorts   []int
	CDPURLs      []string
	Origins      []string
	ProfilesDir  string
	NoBundleScan bool
	NoProbe      bool
	Timeout      time.Duration
	Processes    func(context.Context) ([]byte, error)
	Transport    http.RoundTripper
	Dial         func(ctx context.Context, network, addr string) (net.Conn, error)
}

type Report struct {
	Schema      int     `json:"schema"`
	MinChromium int     `json:"minChromium"`
	Entries     []Entry `json:"entries"`
}

func NewReport(entries []Entry) Report {
	if entries == nil {
		entries = []Entry{}
	}
	return Report{Schema: SchemaVersion, MinChromium: MinChromiumMajor, Entries: entries}
}
