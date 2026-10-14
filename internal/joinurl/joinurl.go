package joinurl

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

var ErrInvalid = errors.New("not a tray join URL: want https://<host>/join/<token>")

func Parse(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, ErrInvalid
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	if Token(u) == "" {
		return nil, ErrInvalid
	}
	return u, nil
}

func Token(u *url.URL) string {
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
