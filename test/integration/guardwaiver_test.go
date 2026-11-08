package integration

import (
	"encoding/json"
	"fmt"
	"testing"
)

type recordTB struct {
	testing.TB
	errs []string
}

func (r *recordTB) Helper()             {}
func (r *recordTB) Logf(string, ...any) {}
func (r *recordTB) Error(args ...any) {
	r.errs = append(r.errs, fmt.Sprint(args...))
}
func (r *recordTB) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}
func (r *recordTB) Fatal(args ...any) {
	r.Error(args...)
}
func (r *recordTB) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
}
func (r *recordTB) Failed() bool { return len(r.errs) > 0 }

func TestGuardWaiversAcceptOnlyDocumentedLegs(t *testing.T) {
	attachOK := guardRow{
		ID: "C3.tab-attach", Control: "fail", Guarded: "fail", Live: "fail",
		Why:    json.RawMessage(`"settle: no request for child"`),
		Errors: []string{`settle: no request for http://ordinary.guard.test/doc within 10000ms`},
	}
	if _, ok := waivedGuardRow(attachOK); !ok {
		t.Fatal(attachOK)
	}
	attachOther := attachOK
	attachOther.Why = json.RawMessage(`"admitted: got false, want true"`)
	attachOther.Errors = nil
	if _, ok := waivedGuardRow(attachOther); ok {
		t.Fatal("accepted non-settle attach failure", attachOther)
	}
	attachLegOnly := attachOK
	attachLegOnly.Why = json.RawMessage(`"admitted: got false, want true"`)
	attachLegOnly.Errors = []string{`{"leg":"guarded","error":"admitted: got false, want true"}`}
	if _, ok := waivedGuardRow(attachLegOnly); ok {
		t.Fatal("accepted guarded leg marker without settle token", attachLegOnly)
	}
	attachGuardedPass := attachOK
	attachGuardedPass.Guarded = "pass"
	if _, ok := waivedGuardRow(attachGuardedPass); ok {
		t.Fatal("accepted guarded pass on tab-attach", attachGuardedPass)
	}

	workerOK := guardRow{
		ID: "C3.tab-worker-leak", Control: "pass", Guarded: "fail", Live: "pass",
		Why: json.RawMessage(`"workerAttached: got true, want false"`),
	}
	if _, ok := waivedGuardRow(workerOK); !ok {
		t.Fatal(workerOK)
	}
	workerGuardedPass := workerOK
	workerGuardedPass.Guarded = "pass"
	if _, ok := waivedGuardRow(workerGuardedPass); ok {
		t.Fatal("accepted guarded pass on worker-leak", workerGuardedPass)
	}
	swOK := workerOK
	swOK.ID = "C3.tab-service-worker-leak"
	if _, ok := waivedGuardRow(swOK); !ok {
		t.Fatal(swOK)
	}

	iframePass := guardRow{
		ID: "C3.tab-iframe-leak", Control: "pass", Guarded: "pass", Live: "pass",
		Observed: json.RawMessage(`{"control":{"admitted":true,"iframeExists":true,"iframeAttached":true},"guarded":{"admitted":true,"iframeExists":true,"iframeAttached":false}}`),
	}
	if _, ok := waivedGuardRow(iframePass); ok {
		t.Fatal("C3.tab-iframe-leak must not be waived", iframePass)
	}

	rec := &recordTB{TB: t}
	checkGuardSummary(rec, &guardSummary{
		Set:     guardNPMPackage + "@" + guardSetVersion(t),
		Pass:    false,
		Missing: json.RawMessage(`[]`),
		Results: []guardRow{attachLegOnly},
	})
	if len(rec.errs) == 0 {
		t.Fatal("checkGuardSummary accepted a guarded leg marker without settle token")
	}
}
