package integration

import (
	"testing"
)

func TestDiagE7InitWorldErrorAndIsolatedForeignWire(t *testing.T) {
	t.Setenv("SLICC_TEST_DUMP_ATTACH_STDERR", "1")
	t.Setenv("SLICC_DEBUG", "1")
	t.Setenv("SLICC_TEST_DIAGE7_TRACE", "1")
	t.Log("DIAGE7 vector steps: E7.init-world-error.script — setup ordinary tab + fail observer for {target}/doc*; Page.enable; Runtime.enable; Page.addScriptToEvaluateOnNewDocument empty source worldName=slicc-error-{tag}; trigger href {target}/doc?n={tag}; settle; expect worldOnError true on unguarded (named world on chrome-error page).")
	t.Log("DIAGE7 vector steps: E7.isolated-foreign.script — setup ordinary tab; Page.enable; Runtime.enable; trigger href {target}/doc?n={tag}; settle; Page.getFrameTree as tree; Page.createIsolatedWorld on frameId from result.frameTree.frame.id worldName=slicc-foreign-{tag} grantUniveralAccess+CSP; expect noContext true and world false guarded.")
	t.Log("DIAGE7 wire: client->guard / guard->chrome / chrome->guard / guard->client for Page.getFrameTree, Runtime.executionContextCreated, Page.createIsolatedWorld, Runtime.evaluate, Runtime.runIfWaitingForDebugger; disposition HELD(docIdentityHold)/FORWARDED/REWRITTEN/DROPPED.")
	for _, id := range []string{"E7.init-world-error.script", "E7.isolated-foreign.script"} {
		id := id
		t.Run(id, func(t *testing.T) {
			sum := runGuard(t, guardRun{
				variant:    "prompt",
				slots:      2,
				only:       id,
				target:     "seven",
				rowTimeout: guardRowTimeout,
			})
			if len(sum.Results) == 0 {
				t.Fatalf("no rows for %s", id)
			}
			for _, r := range sum.Results {
				if r.ID != id {
					t.Errorf("unexpected row %s, want only %s", r.ID, id)
				}
				if r.Target != "seven" {
					t.Errorf("unexpected target %s, want seven", r.Target)
				}
				name := r.ID + " " + r.Target + " " + r.Variant
				t.Logf("DIAGE7 row control=%s guarded=%s live=%s why=%q", r.Control, r.Guarded, r.Live, r.Why)
				for _, err := range r.Errors {
					t.Logf("DIAGE7 %s error: %s", name, err)
				}
				for _, note := range r.Notes {
					t.Logf("DIAGE7 %s note: %s", name, note)
				}
				if r.Observed != nil {
					t.Logf("DIAGE7 %s observed=%s", name, r.Observed)
				}
			}
		})
	}
}
