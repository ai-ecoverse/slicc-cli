package cdp

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

var endpointTransport = newEndpointTransport()

func newEndpointTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DisableCompression = true
	return t
}

func EndpointClient(rt http.RoundTripper, timeout time.Duration) *http.Client {
	if rt == nil {
		rt = endpointTransport
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     rt,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var endpointClient = EndpointClient(nil, 0)

var publicRoutes = []string{"/json/version", "/json/list"}

func Redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "the debugging address"
	}
	out := u.Scheme + "://" + u.Host
	if path := strings.TrimRight(u.Path, "/"); path != "" {
		for _, route := range publicRoutes {
			if strings.HasSuffix(path, route) {
				return out + route
			}
		}
		return out + "/…"
	}
	return out
}

func RedactError(err error, raw string) error {
	if err == nil {
		return nil
	}
	u, perr := url.Parse(raw)
	if perr != nil || strings.Trim(u.Path, "/") == "" {
		return err
	}
	text := err.Error()
	for _, secret := range []string{raw, u.EscapedPath()} {
		text = strings.ReplaceAll(text, secret, Redact(raw))
	}
	if text == err.Error() {
		return err
	}
	return &redactedError{text: text, err: err}
}

type redactedError struct {
	text string
	err  error
}

func (e *redactedError) Error() string { return e.text }

func (e *redactedError) Unwrap() error { return e.err }
