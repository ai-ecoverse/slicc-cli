package jerk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	whatwg "github.com/nlnwa/whatwg-url/url"
)

const (
	MinVersion      = "0.6.0"
	DefaultDuration = 3600
	MaxDuration     = 86400
	Timeout         = 60 * time.Second
	MCPURLPrefix    = "mcp:url:"
	maxMCPURLID     = 2048
	maxOutput       = 1 << 20
	maxString       = 1024
	maxItems        = 1024
)

var (
	providerRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	versionRE  = regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`)

	loopbackHosts = []string{"localhost", "127.0.0.1", "[::1]"}
)

type Jerk struct {
	Path    string
	Version string
}

type Lease struct {
	Credentials map[string]string `json:"credentials"`
	ExpiresAt   string            `json:"expires_at"`
	LeaseID     string            `json:"lease_id,omitempty"`
}

type Result struct {
	Exit      int              `json:"exit"`
	Provider  string           `json:"provider,omitempty"`
	User      string           `json:"user,omitempty"`
	Scopes    []string         `json:"scopes,omitempty"`
	ExpiresAt string           `json:"expires_at,omitempty"`
	Env       []string         `json:"env,omitempty"`
	Message   string           `json:"message"`
	Items     []map[string]any `json:"items,omitempty"`
}

type Outcome struct {
	Lease    *Lease `json:"lease,omitempty"`
	Result   Result `json:"result"`
	Died     bool   `json:"-"`
	TimedOut bool   `json:"-"`
}

type Call struct {
	Args     []string
	Env      []string
	Timeout  time.Duration
	Started  func()
	Progress func(message string)
}

var rowFields = map[string]bool{
	"provider": true, "name": true, "source": true, "requires": true, "cli": true,
	"user": true, "scopes": true, "expires_at": true, "env": true, "id": true,
}

func ValidProvider(p string) bool { return providerRE.MatchString(p) }

func ValidLeaseID(id string) bool {
	return id != "" && len(id) <= 256 && visible(id)
}

func visible(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

type MCPURL struct {
	Scheme string
	Host   string
	Port   uint16
}

func (u MCPURL) String() string { return MCPURLPrefix + u.Scheme + "://" + u.Host }

func IsMCPURL(id string) bool { return strings.HasPrefix(id, MCPURLPrefix) }

func ParseMCPURL(id string) (MCPURL, bool) {
	raw, ok := strings.CutPrefix(id, MCPURLPrefix)
	if !ok || len(id) > maxMCPURLID || !visible(id) || strings.ContainsAny(raw, "?#") {
		return MCPURL{}, false
	}
	u, err := whatwg.Parse(raw)
	if err != nil || u.Href(false) != raw || u.Username() != "" || u.Password() != "" {
		return MCPURL{}, false
	}
	host := u.Hostname()
	var port uint16
	switch {
	case u.Scheme() == "https":
		port = 443
	case u.Scheme() == "http" && slices.Contains(loopbackHosts, host):
		port = 80
	default:
		return MCPURL{}, false
	}
	if p := u.Port(); p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil || n == 0 {
			return MCPURL{}, false
		}
		port = uint16(n)
	}
	return MCPURL{Scheme: u.Scheme(), Host: host, Port: port}, true
}

func Find(dirs ...string) (*Jerk, string) {
	old := ""
	for _, path := range candidates(dirs) {
		v := version(path)
		if v == nil {
			continue
		}
		if atLeast(v[1:], MinVersion) {
			return &Jerk{Path: path, Version: v[0]}, ""
		}
		if old == "" {
			old = v[0]
		}
	}
	return nil, old
}

func version(path string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return nil
	}
	return versionRE.FindStringSubmatch(string(out))
}

func candidates(dirs []string) []string {
	var out []string
	if p, err := exec.LookPath("jerk"); err == nil {
		out = append(out, p)
	}
	for _, d := range dirs {
		for _, name := range []string{"jerk", "jerk.exe"} {
			p := d + string(os.PathSeparator) + name
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() && !sameFile(out, fi) {
				out = append(out, p)
			}
		}
	}
	return out
}

func sameFile(paths []string, fi os.FileInfo) bool {
	for _, p := range paths {
		if o, err := os.Stat(p); err == nil && os.SameFile(o, fi) {
			return true
		}
	}
	return false
}

func atLeast(got []string, min string) bool {
	want := strings.Split(min, ".")
	for i := range 3 {
		g, _ := strconv.Atoi(got[i])
		w, _ := strconv.Atoi(want[i])
		if g != w {
			return g > w
		}
	}
	return true
}

func (j *Jerk) Lease(ctx context.Context, provider string, duration int) Outcome {
	return j.LeaseCall(ctx, provider, Call{Args: []string{provider, "lease", "--via-link"}, Env: []string{"FNOX_LEASE_DURATION=" + strconv.Itoa(duration)}})
}

func (j *Jerk) LeaseCall(ctx context.Context, provider string, c Call) Outcome {
	out := j.Run(ctx, provider, c)
	if out.Died || out.TimedOut || out.Result.Exit != 0 {
		out.Lease = nil
		return out
	}
	if !usable(out.Lease) {
		out.Lease = nil
		out.Result.Exit = 1
		out.Result.Message = "jerk gave no lease with an expiry"
		return out
	}
	if out.Result.ExpiresAt == "" {
		out.Result.ExpiresAt = out.Lease.ExpiresAt
	}
	if len(out.Result.Env) == 0 {
		for k := range out.Lease.Credentials {
			out.Result.Env = append(out.Result.Env, k)
		}
		sort.Strings(out.Result.Env)
	}
	if out.Result.Message == "" {
		out.Result.Message = "leased " + provider
	}
	return out
}

func usable(l *Lease) bool {
	if l == nil || l.ExpiresAt == "" || len(l.Credentials) == 0 {
		return false
	}
	if _, err := time.Parse(time.RFC3339, l.ExpiresAt); err != nil {
		return false
	}
	for k, v := range l.Credentials {
		if k == "" || v == "" {
			return false
		}
	}
	return true
}

func (j *Jerk) Drop(ctx context.Context, provider, leaseID string) Outcome {
	out := j.Run(ctx, provider, Call{Args: []string{provider, "drop", "--lease", "--via-link"}, Env: []string{"FNOX_LEASE_ID=" + leaseID}})
	out.Lease = nil
	if out.Result.Message == "" && !out.Died && !out.TimedOut {
		if out.Result.Exit == 0 {
			out.Result.Message = "dropped the lease"
		} else {
			out.Result.Message = "jerk exited " + strconv.Itoa(out.Result.Exit)
		}
	}
	return out
}

func childEnv(extra []string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "FNOX_LEASE_") {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

func (j *Jerk) Run(ctx context.Context, provider string, c Call) Outcome {
	if c.Timeout <= 0 {
		c.Timeout = Timeout
	}
	tctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(tctx, j.Path, c.Args...)
	cmd.Env = childEnv(c.Env)
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	if err := cmd.Start(); err != nil {
		return Outcome{Died: true, Result: Result{Exit: 1, Provider: provider, Message: "jerk couldn't start"}}
	}
	if c.Started != nil {
		c.Started()
	}
	parsed := make(chan Outcome, 1)
	go func() {
		o := parse(io.LimitReader(pr, maxOutput), c.Progress)
		_, _ = io.Copy(io.Discard, pr)
		parsed <- o
	}()
	err := cmd.Wait()
	_ = pw.Close()
	out := <-parsed
	out.Result.Provider = provider
	var ee *exec.ExitError
	switch {
	case tctx.Err() != nil && ctx.Err() == nil:
		return Outcome{TimedOut: true, Result: Result{Exit: 1, Provider: provider, Message: "jerk timed out"}}
	case errors.As(err, &ee):
		out.Result.Exit = ee.ExitCode()
	case errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil:
		out.Result.Exit = cmd.ProcessState.ExitCode()
	case err != nil:
		out.Died = true
	default:
		out.Result.Exit = 0
	}
	if out.Result.Exit < 0 || out.Result.Exit > 5 {
		out.Died = true
	}
	if out.Died {
		return Outcome{Died: true, Result: Result{Exit: 1, Provider: provider, Message: "jerk died"}}
	}
	return out
}

func parse(r io.Reader, progress func(string)) Outcome {
	var out Outcome
	dec := json.NewDecoder(r)
	for {
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return out
		}
		raw = json.RawMessage(strings.TrimSpace(string(raw)))
		if len(raw) > 0 && raw[0] == '[' {
			out.Result.Items = rows(raw)
			continue
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			continue
		}
		object(&out, obj, progress)
	}
}

func object(out *Outcome, obj map[string]json.RawMessage, progress func(string)) {
	if ev := str(obj["event"]); ev == "progress" {
		if m := str(obj["message"]); m != "" && progress != nil {
			progress(m)
		}
		return
	}
	if _, ok := obj["credentials"]; ok {
		var l Lease
		if b, err := json.Marshal(obj); err == nil && json.Unmarshal(b, &l) == nil {
			out.Lease = &l
		}
		return
	}
	if items, ok := obj["items"]; ok {
		out.Result.Items = rows(items)
	}
	r := &out.Result
	r.User = pick(str(obj["user"]), r.User)
	r.ExpiresAt = pick(str(obj["expires_at"]), r.ExpiresAt)
	r.Message = pick(str(obj["message"]), r.Message)
	if s := strs(obj["scopes"]); s != nil {
		r.Scopes = s
	}
	if s := strs(obj["env"]); s != nil {
		r.Env = s
	}
}

func rows(raw json.RawMessage) []map[string]any {
	var list []map[string]json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	out := []map[string]any{}
	for _, row := range list {
		if len(out) == maxItems {
			break
		}
		clean := map[string]any{}
		for k, v := range row {
			if !rowFields[k] {
				continue
			}
			if val, ok := scalar(v); ok {
				clean[k] = val
			}
		}
		out = append(out, clean)
	}
	return out
}

func scalar(v json.RawMessage) (any, bool) {
	var b bool
	if json.Unmarshal(v, &b) == nil {
		return b, true
	}
	if s := str(v); s != "" {
		return s, true
	}
	if s := strs(v); s != nil {
		return s, true
	}
	return nil, false
}

func str(v json.RawMessage) string {
	var s string
	if v == nil || json.Unmarshal(v, &s) != nil || len(s) > maxString {
		return ""
	}
	return s
}

func strs(v json.RawMessage) []string {
	var s []string
	if v == nil || json.Unmarshal(v, &s) != nil || len(s) > 64 {
		return nil
	}
	for _, x := range s {
		if len(x) > maxString {
			return nil
		}
	}
	return s
}

func pick(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
