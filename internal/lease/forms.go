package lease

import (
	"context"
	_ "embed"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	guarded "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const FormOffer = "ordinary-forms-v1"

//go:embed ordinary-form.js
var formHelper string

var formRef = regexp.MustCompile(`^e[A-Za-z0-9_-]{24}_[0-9]{1,2}$`)

type refSet struct {
	prefix, world string
	at            stamp
	count         int
}

func (s refSet) contains(ref, op string) bool {
	n, ok := strings.CutPrefix(ref, s.prefix+"_")
	i, e := strconv.Atoi(n)
	return ok && s.prefix != "" && e == nil && i >= 0 && i < s.count && strconv.Itoa(i) == n && (i%2 == 0) == (op == "fill")
}

type formParams struct {
	Op       string `json:"op"`
	URL      string `json:"url"`
	Origin   string `json:"origin"`
	End      string `json:"end"`
	Tag      string `json:"tag"`
	Deadline int64  `json:"deadline"`
	Prefix   string `json:"prefix,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Value    string `json:"value,omitempty"`
}
type formResult struct {
	Status, Text string
	Count        int
}

func formArgs(op string, raw json.RawMessage) (ref, value string, e error) {
	switch op {
	case "snapshot":
		return "", "", nil
	case "click":
		var p struct {
			Ref string `json:"ref"`
		}
		if strict(raw, &p) && formRef.MatchString(p.Ref) {
			return p.Ref, "", nil
		}
	case "fill":
		var p struct {
			Ref   string `json:"ref"`
			Value string `json:"value"`
		}
		if strict(raw, &p) && formRef.MatchString(p.Ref) && len(p.Value) > 0 && len(p.Value) <= 512 && !strings.ContainsFunc(p.Value, unicode.IsControl) {
			return p.Ref, p.Value, nil
		}
	}
	return "", "", invalid
}
func (bs *browserSession) callForm(ctx context.Context, t *tab, world string, st stamp, p formParams) (formResult, error) {
	var reply struct{ Result struct{ Value formResult } }
	ctx = guarded.FormDispatch(ctx, t.target, t.session, world, st.f.LoaderID)
	expr := "(()=>{const p=" + literal(p) + ";return " + formHelper + "})()"
	e := bs.c.Call(ctx, t.session, "Runtime.evaluate", map[string]any{"expression": expr, "uniqueContextId": world, "returnByValue": true, "timeout": 1000}, &reply)
	return reply.Result.Value, e
}
func (bs *browserSession) formAction(ctx context.Context, a *active, o *protect.Origins, op string, raw json.RawMessage, live func() bool) (map[string]any, stamp, error) {
	ref, value, e := formArgs(op, raw)
	if e != nil {
		return nil, stamp{}, e
	}
	if op == "snapshot" {
		return bs.formSnapshot(ctx, a, o, live)
	}
	t := a.tab
	old := a.refs
	a.refs = refSet{}
	if !old.contains(ref, op) || a.submitted || t.lost || ctx.Err() != nil || !live() {
		return nil, stamp{}, denied
	}
	st, e := bs.stamp(ctx, t)
	if e != nil || st != old.at || !st.allowed(a.claims, o) {
		return nil, stamp{}, denied
	}
	w, e := bs.world(ctx, t)
	if e != nil || w != old.world {
		return nil, stamp{}, denied
	}
	deadline := min(a.Exp*1000, time.Now().Add(time.Second).UnixMilli())
	params := formParams{Op: op, URL: st.f.URL, Origin: st.f.SecurityOrigin, End: a.formEnd, Tag: a.formTag, Deadline: deadline, Ref: ref, Value: value}
	if !live() || ctx.Err() != nil {
		return nil, stamp{}, retired
	}
	if op == "click" {
		a.submitted = true
	}
	res, e := bs.callForm(ctx, t, w, st, params)
	if e != nil || res.Status != "ok" {
		if res.Status == "stale" {
			return nil, stamp{}, denied
		}
		return nil, stamp{}, unknown
	}
	now, e := bs.stamp(ctx, t)
	if e != nil || now != st || !bs.refWorld(t, w) || !live() || ctx.Err() != nil {
		return nil, stamp{}, unknown
	}
	return map[string]any{}, st, nil
}
func (bs *browserSession) refWorld(t *tab, w string) bool {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return w != "" && t.unique == w
}
func (bs *browserSession) formSnapshot(ctx context.Context, a *active, o *protect.Origins, live func() bool) (map[string]any, stamp, error) {
	a.refs = refSet{}
	t := a.tab
	text, st, e := bs.action(ctx, t, a.claims, o, "snapshot", "", live)
	if e != nil || a.submitted {
		return text, st, e
	}
	w, e := bs.world(ctx, t)
	if e != nil || !live() {
		return nil, stamp{}, denied
	}
	prefix := "e" + id()
	res, e := bs.callForm(ctx, t, w, st, formParams{Op: "snapshot", URL: st.f.URL, Origin: st.f.SecurityOrigin, End: a.formEnd, Tag: a.formTag, Deadline: a.Exp * 1000, Prefix: prefix})
	now, check := bs.stamp(ctx, t)
	if check != nil || now != st || !bs.refWorld(t, w) || !live() {
		return nil, stamp{}, denied
	}
	if e == nil && res.Status == "ok" && res.Count > 0 && res.Count <= 32 && len(res.Text) < 4096 {
		a.refs = refSet{prefix: prefix, world: w, at: st, count: res.Count}
		text["text"] = text["text"].(string) + "\n" + res.Text
	}
	return text, st, nil
}
