package integration

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

type formAction func(string, any) map[string]any

var fixtureRef = regexp.MustCompile(`(?m)^(textbox|button) "(Note|Save)" \[ref=(e[A-Za-z0-9_-]{24}_[0-9]{1,2})\]$`)

func exerciseLeaseForm(t *testing.T, owner *chromeProc, preexisting string, site leaseSite, action formAction) {
	t.Helper()
	targets, e := owner.client.Targets(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	var task string
	for _, ti := range targets {
		if ti.TargetID != preexisting && ti.Type == "page" && strings.HasPrefix(ti.URL, site.Sites.Billing+"/") {
			if task != "" {
				t.Fatal("ambiguous fixture task")
			}
			task = ti.TargetID
		}
	}
	if task == "" {
		t.Fatal("no fixture task tab")
	}
	ss, e := owner.client.Attach(t.Context(), task)
	if e != nil {
		t.Fatal(e)
	}
	eval := func(expr string, out any) {
		t.Helper()
		if e := ss.Evaluate(t.Context(), expr, cdp.EvaluateOptions{ReturnByValue: true}, out); e != nil {
			t.Fatal(e)
		}
	}
	snapshot := func() string {
		t.Helper()
		v := action("snapshot", map[string]any{})
		r, ok := v["result"].(map[string]any)
		if !ok {
			t.Fatal("snapshot refused", v)
		}
		return fmt.Sprint(r["text"])
	}
	refs := func() (string, string) {
		t.Helper()
		s := snapshot()
		m := fixtureRef.FindAllStringSubmatch(s, -1)
		if len(m) != 2 || m[0][1] != "textbox" || m[1][1] != "button" {
			t.Fatalf("expected exactly two top-document refs, got %q", s)
		}
		return m[0][3], m[1][3]
	}
	fill := func(ref, v string) map[string]any { return action("fill", map[string]string{"ref": ref, "value": v}) }
	refuse := func(reply map[string]any) {
		t.Helper()
		if reply["reason"] != "E_SCOPE" {
			t.Fatalf("expected refusal: %v", reply)
		}
	}
	f, _ := refs()
	refs()
	refuse(fill(f, "stale"))
	f, _ = refs()
	eval(`document.querySelector('input').replaceWith(document.querySelector('input').cloneNode(true))`, nil)
	refuse(fill(f, "stale"))
	f, _ = refs()
	eval(`document.body.insertAdjacentHTML('beforeend','<div id="shield" style="position:fixed;inset:0;z-index:999"></div>')`, nil)
	refuse(fill(f, "covered"))
	if s := snapshot(); fixtureRef.MatchString(s) {
		t.Fatal("overlay offered refs")
	}
	eval(`document.querySelector('#shield').remove()`, nil)
	f, _ = refs()
	eval(`document.querySelector('iframe').focus()`, nil)
	refuse(fill(f, "wrong focus"))
	eval(`document.querySelector('input').focus()`, nil)
	f, _ = refs()
	eval(`history.replaceState(null,'','#another');history.replaceState(null,'','#paid')`, nil)
	refuse(fill(f, "stale route"))
	f, _ = refs()
	if e := ss.Call(t.Context(), "Page.navigate", map[string]string{"url": site.Sites.Billing + "/invoices/1042?new-document"}, nil); e != nil {
		t.Fatal(e)
	}
	for end := time.Now().Add(5 * time.Second); ; {
		var ready bool
		e := ss.Evaluate(t.Context(), "(async()=>{if (window.invoiceReady) await window.invoiceReady;return document.readyState==='complete' && !!document.querySelector('form')})()", cdp.EvaluateOptions{AwaitPromise: true, ReturnByValue: true}, &ready)
		if e == nil && ready {
			break
		}
		if time.Now().After(end) {
			t.Fatal("fixture navigation did not settle")
		}
		time.Sleep(20 * time.Millisecond)
	}
	refuse(fill(f, "new loader"))
	f, _ = refs()
	eval(`document.querySelector('input').type='password'`, nil)
	refuse(fill(f, "secret"))
	if fixtureRef.MatchString(snapshot()) {
		t.Fatal("password eligible")
	}
	eval(`document.querySelector('input').type='file'`, nil)
	if fixtureRef.MatchString(snapshot()) {
		t.Fatal("file eligible")
	}
	eval(`document.querySelector('input').type='text'`, nil)
	f, _ = refs()
	eval(`document.querySelector('button').formAction='https://outside.invalid/'`, nil)
	refuse(fill(f, "changed form"))
	if fixtureRef.MatchString(snapshot()) {
		t.Fatal("submit override eligible")
	}
	eval(`document.querySelector('button').removeAttribute('formaction');document.querySelector('form').insertAdjacentHTML('beforeend','<input type=hidden name=other>')`, nil)
	if fixtureRef.MatchString(snapshot()) {
		t.Fatal("extra form field eligible")
	}
	eval(`document.querySelector('[name=other]').remove()`, nil)
	f, _ = refs()
	if v := action("fill", map[string]any{"ref": f, "value": "x", "selector": "input"}); v["reason"] != "E_INVALID" {
		t.Fatal("unexpected caller selector", v)
	}
	if v := fill(f, strings.Repeat("x", 513)); v["reason"] != "E_INVALID" {
		t.Fatal("unbounded fill", v)
	}
	if v := action("click", map[string]any{"x": 10, "y": 10}); v["reason"] != "E_INVALID" {
		t.Fatal("coordinates", v)
	}
	if v := action("evaluate", map[string]any{}); v["reason"] != "E_UNSUPPORTED" {
		t.Fatal("unrestricted action", v)
	}
	var empty bool
	eval(`document.querySelector('input').value===''`, &empty)
	if !empty {
		t.Fatal("blocked action changed field")
	}
	f, _ = refs()
	eval(`document.querySelector("input").addEventListener("input",()=>history.replaceState(null,"","#input-handler-ran"),{once:true})`, nil)
	if v := fill(f, "fixture first edit"); v["reason"] != "E_UNKNOWN_OUTCOME" {
		t.Fatalf("expected uncertain outcome after SPA history changed in input handler: %v", v)
	}
	refuse(fill(f, "no replay"))
	f, _ = refs()
	if reply := fill(f, "fixture note"); reply["type"] != "result" {
		t.Fatal("fill", reply)
	}
	refuse(fill(f, "replay"))
	_, button := refs()
	reply := action("click", map[string]string{"ref": button})
	if reply["type"] != "result" && reply["reason"] != "E_UNKNOWN_OUTCOME" {
		t.Fatal("click", reply)
	}
	if site.submitted != nil {
		select {
		case v := <-site.submitted:
			if v != "note=fixture+note" {
				t.Fatal("unexpected POST body")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("form did not post")
		}
	}
	var saved bool
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
		r := action("snapshot", map[string]any{})
		if strings.Contains(fmt.Sprint(r), "Fixture note saved") {
			saved = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !saved {
		t.Fatal("no saved result document")
	}
	refuse(action("click", map[string]string{"ref": button}))
	if r := action("goto", map[string]string{"url": site.Sites.Billing + "/"}); r["type"] != "result" {
		t.Fatal(r)
	}
	if fixtureRef.MatchString(snapshot()) {
		t.Fatal("second submit budget")
	}
}
