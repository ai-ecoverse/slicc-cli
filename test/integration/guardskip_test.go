package integration

import (
	"strings"
	"testing"
)

func documentedGuardSkip(r guardRow) bool {
	if r.Control != "skip" || r.Guarded != "skip" || r.Live != "skip" {
		return false
	}
	if r.Reason == "not-applicable" || strings.HasPrefix(r.Reason, "not-applicable: ") {
		return true
	}
	return r.Reason == "not-supported:siteIsolationOff" && (r.ID == "C4.iframe-preattach.blank.shared" || r.ID == "C4.iframe-preattach.srcdoc.shared")
}

func TestGuardSkipsRequireExplicitConsistentReasons(t *testing.T) {
	base := guardRow{ID: "C4.iframe-preattach.blank.shared", Control: "skip", Guarded: "skip", Live: "skip", Reason: "not-supported:siteIsolationOff"}
	if !documentedGuardSkip(base) {
		t.Fatal(base)
	}
	for _, reason := range []string{"", "not-applicable-fake", "not-supported:unknown", "unknown"} {
		r := base
		r.Reason = reason
		if documentedGuardSkip(r) {
			t.Fatal("accepted unexplained skip", r)
		}
	}
	for _, status := range []string{"pass", "fail", "incomplete", "not-run"} {
		for _, field := range []string{"control", "guarded", "live"} {
			r := base
			switch field {
			case "control":
				r.Control = status
			case "guarded":
				r.Guarded = status
			case "live":
				r.Live = status
			}
			if documentedGuardSkip(r) {
				t.Fatal("accepted inconsistent skip", r)
			}
		}
	}
	r := base
	r.ID = "C1.href"
	if documentedGuardSkip(r) {
		t.Fatal("accepted unapproved unsupported vector", r)
	}
	r.Reason = "not-applicable"
	if !documentedGuardSkip(r) {
		t.Fatal(r)
	}
}
