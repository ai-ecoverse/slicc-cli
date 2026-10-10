package tray

import (
	"net/http"
	"net/url"
	"strings"
)

const SuccessorVersionRel = "successor-version"

func SuccessorFromLinkHeader(header http.Header) string {
	if header == nil {
		return ""
	}
	raw := strings.ReplaceAll(strings.Join(header.Values("Link"), ", "), "\n", ", ")
	for _, value := range splitOutsideQuotes(raw, ',') {
		if !strings.HasPrefix(value, "<") {
			continue
		}
		end := strings.Index(value, ">")
		if end == -1 || !hasSuccessorRel(value[end+1:]) {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(value[1:end]))
		if err != nil || !u.IsAbs() || u.Host == "" {
			return ""
		}
		return u.String()
	}
	return ""
}

func splitOutsideQuotes(input string, sep byte) []string {
	var out []string
	start := 0
	inQuotes, inAngle := false, false
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if inQuotes {
			switch ch {
			case '\\':
				i++
			case '"':
				inQuotes = false
			}
			continue
		}
		switch {
		case ch == '"':
			inQuotes = true
		case ch == '<':
			inAngle = true
		case ch == '>':
			inAngle = false
		case ch == sep && !inAngle:
			out = append(out, strings.TrimSpace(input[start:i]))
			start = i + 1
		}
	}
	out = append(out, strings.TrimSpace(input[start:]))
	kept := out[:0]
	for _, v := range out {
		if v != "" {
			kept = append(kept, v)
		}
	}
	return kept
}

func hasSuccessorRel(params string) bool {
	for _, param := range splitOutsideQuotes(params, ';') {
		name, value, ok := strings.Cut(param, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "rel") {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
			value = value[1 : len(value)-1]
		}
		for _, token := range strings.Fields(value) {
			if strings.EqualFold(token, SuccessorVersionRel) {
				return true
			}
		}
	}
	return false
}
