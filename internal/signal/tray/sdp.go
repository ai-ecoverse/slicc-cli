package tray

import (
	"fmt"
	"strings"
)

func SummarizeSDP(sdp string) string {
	var media []string
	candidates := 0
	fingerprint := ""
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "m="):
			kind, _, _ := strings.Cut(strings.TrimPrefix(line, "m="), " ")
			media = append(media, kind)
		case strings.HasPrefix(line, "a=candidate:"):
			candidates++
		case fingerprint == "" && strings.HasPrefix(line, "a=fingerprint:"):
			fingerprint = strings.TrimPrefix(line, "a=fingerprint:")
		}
	}
	kinds := "none"
	if len(media) > 0 {
		kinds = strings.Join(media, ",")
	}
	out := fmt.Sprintf("%d bytes, %d media (%s), %d candidates", len(sdp), len(media), kinds, candidates)
	if fingerprint != "" {
		out += ", fingerprint " + fingerprint
	}
	return out
}
