package tray

import (
	"errors"
	"net/url"
	"strings"
	"sync"

	"github.com/ai-ecoverse/slicc-cli/internal/joinurl"
)

var ErrInvalidJoinURL = joinurl.ErrInvalid

func ParseJoinURL(raw string) (*url.URL, error) { return joinurl.Parse(raw) }

func IsJoinURL(raw string) bool {
	_, err := joinurl.Parse(raw)
	return err == nil
}

func JoinToken(u *url.URL) string { return joinurl.Token(u) }

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

var ErrJoinOrigin = errors.New("join URL is not on the hub origin this node follows")

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "http") {
		return "80"
	}
	return "443"
}
