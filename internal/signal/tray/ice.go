package tray

import (
	"net"
	"strings"
)

const (
	cloudflareTURNHost = "turn.cloudflare.com"
	cloudflareTLS443   = "turns:turn.cloudflare.com:443?transport=tcp"
)

func PrepareICEServers(in []ICEServer) ([]ICEServer, bool) {
	if len(in) == 0 {
		return in, false
	}
	out := make([]ICEServer, len(in))
	added := false
	for i, server := range in {
		urls := append([]string(nil), server.URLs...)
		if cloudflareTLS443Missing(urls, server.Username, server.Credential) {
			urls = append(urls, cloudflareTLS443)
			added = true
		}
		out[i] = ICEServer{URLs: urls, Username: server.Username, Credential: server.Credential}
	}
	return out, added
}

func cloudflareTLS443Missing(urls []string, username, credential string) bool {
	if username == "" && credential == "" {
		return false
	}
	cloudflare := false
	for _, raw := range urls {
		scheme, host, port, ok := splitICEURL(raw)
		if !ok || !strings.EqualFold(host, cloudflareTURNHost) {
			continue
		}
		if scheme == "turns" && port == "443" {
			return false
		}
		if scheme == "turn" || scheme == "turns" {
			cloudflare = true
		}
	}
	return cloudflare
}

func splitICEURL(raw string) (scheme, host, port string, ok bool) {
	scheme, rest, found := strings.Cut(strings.TrimSpace(raw), ":")
	if !found {
		return "", "", "", false
	}
	scheme = strings.ToLower(scheme)
	switch scheme {
	case "stun", "stuns", "turn", "turns":
	default:
		return "", "", "", false
	}
	rest = strings.TrimPrefix(rest, "//")
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest = rest[:i]
	}
	if h, p, err := net.SplitHostPort(rest); err == nil {
		return scheme, h, p, h != ""
	}
	if rest == "" || strings.Contains(rest, ":") {
		return "", "", "", false
	}
	return scheme, rest, "", true
}
