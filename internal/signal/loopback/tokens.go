package loopback

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"sync"
	"time"
)

const (
	TokenLifetime = 10 * time.Minute
	PairLimit     = 10
	PairWindow    = time.Minute
)

type tokens struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func (t *tokens) mint(now time.Time) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]time.Time{}
	}
	t.m[tok] = now.Add(TokenLifetime)
	return tok
}

func (t *tokens) burn(tok string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	hit := ""
	for k, exp := range t.m {
		if now.After(exp) {
			delete(t.m, k)
			continue
		}
		if subtle.ConstantTimeCompare([]byte(k), []byte(tok)) == 1 {
			hit = k
		}
	}
	if hit == "" {
		return false
	}
	delete(t.m, hit)
	return true
}

func (t *tokens) valid(tok string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	exp, ok := t.m[tok]
	return ok && !now.After(exp)
}

type limiter struct {
	mu    sync.Mutex
	times []time.Time
	max   int
	per   time.Duration
}

func (l *limiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.times[:0]
	for _, t := range l.times {
		if now.Sub(t) < l.per {
			kept = append(kept, t)
		}
	}
	l.times = kept
	if len(l.times) >= l.max {
		return false
	}
	l.times = append(l.times, now)
	return true
}
