package cdp

import (
	"context"
	"errors"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

const (
	Kind         = "cdp"
	Cap          = "cdp"
	UpdateKind   = "browsers"
	UpdateCap    = "browsers"
	MaxMessage   = 64 << 20
	DialTimeout  = 10 * time.Second
	ProbeTimeout = 5 * time.Second
)

var ErrUnknown = errors.New("no such browser")

type entry struct {
	info   link.Browser
	key    string
	raw    string
	ep     browser.Endpoint
	relays map[*relay]struct{}
}

type Registry struct {
	logf  func(format string, args ...any)
	probe func(ctx context.Context, raw string) (browser.Endpoint, error)
	onAdd func(ctx context.Context, id string, hostports []string)

	mu      sync.Mutex
	entries []*entry
	known   map[string]string
	taken   map[string]bool
	gone    map[string]*entry
	links   map[*link.Link]*pusher

	version      int
	helloVersion int
}

func NewRegistry(logf func(format string, args ...any)) *Registry {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Registry{
		logf:  logf,
		probe: probe,
		known: map[string]string{},
		taken: map[string]bool{},
		gone:  map[string]*entry{},
		links: map[*link.Link]*pusher{},
	}
}

func probe(ctx context.Context, raw string) (browser.Endpoint, error) {
	return browser.ProbeEndpoint(ctx, raw, browser.Options{NoProbe: true, Timeout: ProbeTimeout})
}

func (r *Registry) OnAdd(fn func(ctx context.Context, id string, hostports []string)) {
	r.mu.Lock()
	r.onAdd = fn
	r.mu.Unlock()
}

func endpointHostPorts(key string, ep browser.Endpoint) []string {
	out := []string{key}
	for _, raw := range []string{ep.CDP, ep.WebSocketURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || u.Port() == "" {
			continue
		}
		hp := net.JoinHostPort(strings.ToLower(u.Hostname()), u.Port())
		if !slices.Contains(out, hp) {
			out = append(out, hp)
		}
	}
	return out
}

func Register(n *node.Node, r *Registry) {
	n.Handle(Cap, Kind, r.Handler())
	n.OnHello(r.hello)
}

func endpointKey(raw string) (string, error) {
	base, _, err := browser.NormalizeCDPURL(raw)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, u.Port()), nil
}

func (r *Registry) Add(ctx context.Context, raw string, seven bool) (link.Browser, error) {
	key, err := endpointKey(raw)
	if err != nil {
		return link.Browser{}, err
	}
	r.mu.Lock()
	for _, e := range r.entries {
		if e.key == key {
			changed := seven && !e.info.Seven
			e.info.Seven = e.info.Seven || seven
			info := e.info
			r.mu.Unlock()
			if changed {
				r.changed()
			}
			return info, nil
		}
	}
	r.mu.Unlock()
	ep, err := r.probe(ctx, raw)
	if err != nil {
		return link.Browser{}, err
	}
	r.mu.Lock()
	id, ok := r.known[key]
	if !ok {
		id = r.newIDLocked(kindOf(ep))
		r.known[key] = id
	}
	e := &entry{key: key, raw: raw, ep: ep, relays: map[*relay]struct{}{}, info: link.Browser{ID: id, Name: ep.Name, Version: ep.Version, Seven: seven}}
	delete(r.gone, id)
	r.entries = append(r.entries, e)
	onAdd := r.onAdd
	r.mu.Unlock()
	if onAdd != nil {
		onAdd(ctx, id, endpointHostPorts(key, ep))
	}
	r.logf("cdp: browser %s is %s at %s", id, ep.Name, ep.CDP)
	r.changed()
	return e.info, nil
}

func (r *Registry) newIDLocked(kind string) string {
	if !r.taken[kind] {
		r.taken[kind] = true
		return kind
	}
	for i := 2; ; i++ {
		id := kind + "-" + strconv.Itoa(i)
		if !r.taken[id] {
			r.taken[id] = true
			return id
		}
	}
}

func (r *Registry) Remove(id string) bool {
	r.mu.Lock()
	_, wasGone := r.gone[id]
	delete(r.gone, id)
	r.mu.Unlock()
	e := r.take(id)
	if e == nil {
		return wasGone
	}
	r.closeRelays(e, "ECONNRESET: browser removed")
	r.changed()
	return true
}

func (r *Registry) take(id string) *entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, e := range r.entries {
		if e.info.ID == id {
			r.entries = append(r.entries[:i:i], r.entries[i+1:]...)
			return e
		}
	}
	return nil
}

func (r *Registry) closeRelays(e *entry, reason string) {
	r.mu.Lock()
	relays := make([]*relay, 0, len(e.relays))
	for rl := range e.relays {
		relays = append(relays, rl)
	}
	r.mu.Unlock()
	for _, rl := range relays {
		rl.kill(reason)
	}
}

func (r *Registry) List() []link.Browser {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]link.Browser, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.info)
	}
	return out
}

func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

func (r *Registry) lookup(id string) *entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries {
		if e.info.ID == id {
			return e
		}
	}
	return nil
}

func (r *Registry) track(e *entry, rl *relay) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.entries {
		if x == e {
			e.relays[rl] = struct{}{}
			return true
		}
	}
	return false
}

func (r *Registry) untrack(e *entry, rl *relay) {
	r.mu.Lock()
	delete(e.relays, rl)
	r.mu.Unlock()
}

func (r *Registry) Watch(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 2 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	misses := map[string]int{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r.watchOnce(ctx, misses)
	}
}

func (r *Registry) watchOnce(ctx context.Context, misses map[string]int) {
	r.mu.Lock()
	live := append([]*entry(nil), r.entries...)
	gone := make([]*entry, 0, len(r.gone))
	for _, e := range r.gone {
		gone = append(gone, e)
	}
	r.mu.Unlock()
	for _, e := range live {
		if answers(ctx, e.key) {
			misses[e.info.ID] = 0
			continue
		}
		misses[e.info.ID]++
		if misses[e.info.ID] < 2 {
			continue
		}
		if r.take(e.info.ID) != nil {
			r.mu.Lock()
			r.gone[e.info.ID] = e
			r.mu.Unlock()
			r.logf("cdp: browser %s stopped answering", e.info.ID)
			r.closeRelays(e, "ECONNRESET: browser quit")
			r.changed()
		}
	}
	for _, e := range gone {
		if !answers(ctx, e.key) {
			continue
		}
		if _, err := r.Add(ctx, e.raw, e.info.Seven); err == nil {
			misses[e.info.ID] = 0
		}
	}
}

func answers(ctx context.Context, hostport string) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", hostport)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

var kindProducts = map[string]string{
	"chrome":         "chrome",
	"headlesschrome": "chrome",
	"edg":            "edge",
	"opr":            "opera",
	"chromium":       "chromium",
}

var productName = regexp.MustCompile(`^([^/]+)/`)

func kindOf(ep browser.Endpoint) string {
	if ep.Browser != "" {
		return sanitizeKind(ep.Browser)
	}
	if strings.Contains(ep.UserAgent, " Electron/") {
		return "electron"
	}
	if m := productName.FindStringSubmatch(ep.Product); m != nil {
		if k, ok := kindProducts[strings.ToLower(m[1])]; ok {
			return k
		}
		if k := sanitizeKind(m[1]); k != "" {
			return k
		}
	}
	return "chromium"
}

func sanitizeKind(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	k := strings.TrimRight(b.String(), "-")
	if k == "" {
		return "chromium"
	}
	return k
}
