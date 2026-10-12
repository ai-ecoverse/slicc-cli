package http

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

func Credentialed(open json.RawMessage) bool {
	var o map[string]json.RawMessage
	if json.Unmarshal(open, &o) != nil {
		return true
	}
	v, ok := o["credentialed"]
	if !ok {
		return false
	}
	switch strings.TrimSpace(string(v)) {
	case "false", "null":
		return false
	}
	return true
}

func HTTPS443(u *url.URL) bool {
	return u.Scheme == "https" && (u.Port() == "" || u.Port() == "443")
}

func Scrub(err error, u *url.URL) string {
	var ue *url.Error
	for errors.As(err, &ue) {
		err = ue.Err
	}
	if err == nil {
		return "request failed"
	}
	msg := err.Error()
	for _, s := range []string{u.String(), u.RequestURI(), u.RawQuery, u.EscapedPath(), u.Path, u.Fragment} {
		if len(s) > 1 {
			msg = strings.ReplaceAll(msg, s, "")
		}
	}
	return msg
}
