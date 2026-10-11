package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strconv"
	"strings"
	"unicode"
)

type procHit struct {
	pid  int
	port int
	dir  string
}

func parseProcessTable(out []byte, windows bool, self int) []procHit {
	if windows {
		return parseCIM(out, self)
	}
	return parsePS(string(out), self)
}

func parsePS(text string, self int) []procHit {
	var hits []procHit
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		i := strings.IndexFunc(line, unicode.IsSpace)
		if i < 0 {
			continue
		}
		pid, err := strconv.Atoi(line[:i])
		if err != nil || pid <= 0 || pid == self {
			continue
		}
		if h, ok := argvHit(pid, strings.TrimSpace(line[i:])); ok {
			hits = append(hits, h)
		}
	}
	return hits
}

func parseCIM(out []byte, self int) []procHit {
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil
	}
	var rows []map[string]any
	if out[0] == '{' {
		var one map[string]any
		if json.Unmarshal(out, &one) != nil {
			return nil
		}
		rows = append(rows, one)
	} else if json.Unmarshal(out, &rows) != nil {
		return nil
	}
	var hits []procHit
	for _, r := range rows {
		pid := jsonInt(r["ProcessId"])
		cmd, ok := r["CommandLine"].(string)
		if !ok || pid <= 0 || pid == self {
			continue
		}
		if h, ok := argvHit(pid, cmd); ok {
			hits = append(hits, h)
		}
	}
	return hits
}

func jsonInt(v any) int {
	switch t := v.(type) {
	case float64:
		if t >= 0 && t == float64(int(t)) {
			return int(t)
		}
	case string:
		if n, err := strconv.Atoi(t); err == nil {
			return n
		}
	}
	return -1
}

func argvHit(pid int, cmd string) (procHit, bool) {
	if isWrapper(cmd) || displayExecutable(cmd) == "" {
		return procHit{}, false
	}
	port, ok := debuggingPort(cmd)
	if !ok {
		return procHit{}, false
	}
	return procHit{pid: pid, port: port, dir: flagValue(cmd, catalog.Discovery.Process.UserDataDirFlag)}, true
}

func headExecutable(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) || strings.HasPrefix(cmd, "'") {
		return quotedToken(cmd)
	}
	if f := strings.Fields(cmd); len(f) > 0 {
		return f[0]
	}
	return ""
}

func displayExecutable(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) || strings.HasPrefix(cmd, "'") {
		if q := quotedToken(cmd); q != "" {
			return q
		}
	}
	var parts []string
	for _, f := range strings.Fields(cmd) {
		if strings.HasPrefix(f, "-") {
			break
		}
		parts = append(parts, f)
	}
	return strings.Join(parts, " ")
}

func quotedToken(s string) string {
	if s == "" {
		return ""
	}
	rest := s[1:]
	if i := strings.IndexByte(rest, s[0]); i >= 0 {
		return rest[:i]
	}
	return rest
}

func isWrapper(cmd string) bool {
	head := headExecutable(cmd)
	if head == "" {
		return false
	}
	base := strings.ToLower(head[strings.LastIndexAny(head, `/\`)+1:])
	for _, suffix := range catalog.Discovery.Process.WrapperSuffixes {
		if s, ok := strings.CutSuffix(base, suffix); ok {
			base = s
			break
		}
	}
	for _, w := range catalog.Discovery.Process.Wrappers {
		if strings.EqualFold(base, w) {
			return true
		}
	}
	return false
}

func debuggingPort(cmd string) (int, bool) {
	marker := catalog.Discovery.Process.PortFlag
	rest := cmd
	for {
		i := strings.Index(rest, marker)
		if i < 0 {
			return 0, false
		}
		after := rest[i+len(marker):]
		if digits, ok := portDigits(after); ok {
			n, err := strconv.Atoi(digits)
			if err != nil || n > 65535 {
				return 0, false
			}
			return n, true
		}
		rest = rest[i+1:]
	}
}

func portDigits(after string) (string, bool) {
	var body string
	switch {
	case strings.HasPrefix(after, "="):
		body = after[1:]
	case after != "" && unicode.IsSpace(rune(after[0])):
		body = strings.TrimLeftFunc(after, unicode.IsSpace)
	default:
		return "", false
	}
	n := 0
	for n < len(body) && body[n] >= '0' && body[n] <= '9' {
		n++
	}
	if n == 0 {
		return "", false
	}
	return body[:n], true
}

func flagValue(cmd, marker string) string {
	start := 0
	for {
		rel := strings.Index(cmd[start:], marker)
		if rel < 0 {
			return ""
		}
		i := start + rel
		after := i + len(marker)
		if i > 0 && !unicode.IsSpace(rune(cmd[i-1])) {
			start = i + 1
			continue
		}
		rest := cmd[after:]
		switch {
		case strings.HasPrefix(rest, "="):
			return takeToken(rest[1:])
		case rest != "" && unicode.IsSpace(rune(rest[0])):
			return takeToken(rest)
		}
		start = after
	}
}

func takeToken(s string) string {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	if strings.HasPrefix(s, `"`) || strings.HasPrefix(s, "'") {
		return quotedToken(s)
	}
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

func underLegacyProfiles(dir string) bool {
	legacy := catalog.Profiles.LegacyRoot
	parts := strings.FieldsFunc(dir, func(r rune) bool { return r == '/' || r == '\\' })
	for i := 0; i+len(legacy) <= len(parts); i++ {
		match := true
		for j, l := range legacy {
			if !strings.EqualFold(parts[i+j], l) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func processCandidates(ctx context.Context, o Options, d discovery) []candidate {
	if !d.process {
		return nil
	}
	table := o.Processes
	if table == nil {
		table = processTable
	}
	out, err := table(ctx)
	if err != nil {
		return nil
	}
	var cands []candidate
	for _, h := range parseProcessTable(out, runtime.GOOS == "windows", os.Getpid()) {
		if h.port > 0 {
			cands = append(cands, candidate{base: "http://127.0.0.1:" + strconv.Itoa(h.port), source: SourceProcess, dir: h.dir})
			continue
		}
		if h.dir == "" || underLegacyProfiles(h.dir) {
			continue
		}
		port, ws, err := ReadActivePort(h.dir)
		if err != nil {
			continue
		}
		cands = append(cands, candidate{base: "http://127.0.0.1:" + strconv.Itoa(port), wsPath: ws, source: SourceProcess, dir: h.dir})
	}
	return cands
}
