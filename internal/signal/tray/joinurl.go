package tray

import (
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
)

var ErrInvalidJoinURL = errors.New("not a tray join URL: want https://<host>/join/<token>")

func ParseJoinURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, ErrInvalidJoinURL
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return nil, ErrInvalidJoinURL
		}
	default:
		return nil, ErrInvalidJoinURL
	}
	if JoinToken(u) == "" {
		return nil, ErrInvalidJoinURL
	}
	return u, nil
}

func IsJoinURL(raw string) bool {
	_, err := ParseJoinURL(raw)
	return err == nil
}

func JoinToken(u *url.URL) string {
	rest, ok := strings.CutPrefix(u.EscapedPath(), "/join/")
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return ""
	}
	return rest
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type redactor struct {
	mu      sync.Mutex
	secrets []string
}

func (r *redactor) add(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.secrets = append(r.secrets, raw)
	if u, err := url.Parse(raw); err == nil {
		if tok, ok := strings.CutPrefix(u.EscapedPath(), "/join/"); ok {
			tok = strings.Trim(tok, "/")
			if tok != "" {
				r.secrets = append(r.secrets, tok)
				if un, err := url.PathUnescape(tok); err == nil && un != tok {
					r.secrets = append(r.secrets, un)
				}
				if i := strings.LastIndexByte(tok, '.'); i >= 0 && i+1 < len(tok) {
					r.secrets = append(r.secrets, tok[i+1:])
				}
			}
		}
	}
}

func (r *redactor) redact(s string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, secret := range r.secrets {
		if len(secret) >= 4 {
			s = strings.ReplaceAll(s, secret, "<redacted>")
		}
	}
	return s
}
