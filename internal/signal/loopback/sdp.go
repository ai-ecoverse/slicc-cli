package loopback

import (
	"errors"
	"regexp"
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/signal"
)

var (
	ErrNoFingerprint = errors.New("loopback: the offer has no single sha-256 fingerprint")
	hexFingerprint   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func Fingerprint(sdp string) (string, error) {
	fp := ""
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimRight(line, "\r")
		rest, ok := strings.CutPrefix(line, "a=fingerprint:")
		if !ok {
			continue
		}
		alg, value, ok := strings.Cut(rest, " ")
		if !ok || !strings.EqualFold(alg, "sha-256") {
			continue
		}
		v := NormalizeFingerprint(value)
		if !hexFingerprint.MatchString(v) || (fp != "" && fp != v) {
			return "", ErrNoFingerprint
		}
		fp = v
	}
	if fp == "" {
		return "", ErrNoFingerprint
	}
	return fp, nil
}

func withCandidates(sdp string, cands []*signal.Candidate) string {
	eol := "\r\n"
	if !strings.Contains(sdp, "\r\n") {
		eol = "\n"
	}
	lines := strings.Split(strings.TrimRight(sdp, "\r\n"), eol)
	var sections [][]string
	cur := []string{}
	for _, l := range lines {
		if strings.HasPrefix(l, "m=") {
			sections = append(sections, cur)
			cur = []string{}
		}
		cur = append(cur, l)
	}
	sections = append(sections, cur)
	for _, c := range cands {
		i := mediaIndex(sections, c)
		if i < 1 || i >= len(sections) {
			continue
		}
		sections[i] = append(sections[i], "a="+strings.TrimPrefix(c.Candidate, "a="))
	}
	var b strings.Builder
	for i, s := range sections {
		for _, l := range s {
			b.WriteString(l)
			b.WriteString(eol)
		}
		if i > 0 && !contains(s, "a=end-of-candidates") {
			b.WriteString("a=end-of-candidates")
			b.WriteString(eol)
		}
	}
	return b.String()
}

func mediaIndex(sections [][]string, c *signal.Candidate) int {
	if c.SDPMid != nil {
		for i := 1; i < len(sections); i++ {
			if contains(sections[i], "a=mid:"+*c.SDPMid) {
				return i
			}
		}
	}
	if c.SDPMLineIndex != nil {
		return int(*c.SDPMLineIndex) + 1
	}
	return 1
}

func contains(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
