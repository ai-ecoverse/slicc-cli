package integration

import (
	"os"
	"strconv"
	"testing"
)

func TestCDPC3TabAttachSettleDiag(t *testing.T) {
	const id = "C3.tab-attach"
	n := 1
	if raw := os.Getenv("SLICC_C3_TAB_ATTACH_DIAG_N"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 2 {
			t.Fatalf("SLICC_C3_TAB_ATTACH_DIAG_N=%q want 1 or 2", raw)
		}
		n = v
	}
	t.Setenv("SLICC_TEST_DUMP_ATTACH_STDERR", "1")
	t.Setenv("SLICC_DEBUG", "1")
	t.Setenv("SLICC_TEST_DIAGC3_TRACE", "1")
	t.Log("DIAGC3 memo script: setup opens ordinary opener page {ordinary}/doc?n=opener-{tag}; browser Target.setAutoAttach autoAttach=true waitForDebuggerOnStart=true flatten=true filter=[{type:page,exclude:true},{}] requires stockSetup; children where type=page set Target.setAutoAttach filter=[{}]; trigger Runtime.evaluate window.open({target}/doc?n={tag}); settle required served that URL 5000ms; Page.navigate child session to child-{tag}; settle child URL 5000ms; observe admitted/tab/driven. Control uses unguarded client (vector.control.via=unguarded); guarded uses link-guard client. settle fail means fixture saw no request for the URL.")
	t.Log("DIAGC3 memo Q1 popup create/announce: chrome->guard Target.targetCreated/attachedToTarget/targetInfoChanged with opener; guard->client forward or hold; see attach-decision lines.")
	t.Log("DIAGC3 memo Q2 waitForDebugger: holding/released/resumed/detached auto-attached; hold-empty-url resumes via Runtime.runIfWaitingForDebugger; client adopt also resumes after attachedToTarget.")
	t.Log("DIAGC3 memo Q3 document Fetch: Fetch.requestPaused then Fetch.continueRequest or Fetch.failRequest for popup Document url.")
	t.Log("DIAGC3 memo Q4 stockSetup: capability gate only (guard-runner supported()); both legs get stockSetup:true from runGuard; control client is unguarded Chrome, guarded client is through relay; browser setAutoAttach excludes type=page.")
	t.Log("DIAGC3 memo stock popups: TestStockClientsDriveThroughTheLinkGuard / stockguard.mjs click #pop target=_blank through the link guard; C3.tab-attach is CDP waitForDebugger+tab filter, not that Playwright path.")
	for _, variant := range []string{"prompt", "lagging"} {
		variant := variant
		for i := 0; i < n; i++ {
			i := i
			t.Run(variant+"/"+strconv.Itoa(i), func(t *testing.T) {
				sum := runGuard(t, guardRun{
					variant:    variant,
					slots:      2,
					only:       id,
					rowTimeout: guardRowTimeout,
					diagC3:     true,
				})
				if len(sum.Results) == 0 {
					t.Fatalf("no rows for %s", id)
				}
				for _, r := range sum.Results {
					if r.ID != id {
						t.Errorf("unexpected row %s, want only %s", r.ID, id)
					}
					name := r.ID + " " + r.Target + " " + r.Variant
					t.Logf("DIAGC3 row trial=%d control=%s guarded=%s live=%s why=%s", i, r.Control, r.Guarded, r.Live, r.Why)
					for _, err := range r.Errors {
						t.Logf("DIAGC3 %s error: %s", name, err)
					}
					for _, note := range r.Notes {
						t.Logf("DIAGC3 %s note: %s", name, note)
					}
					if r.Observed != nil {
						t.Logf("DIAGC3 %s observed=%s", name, r.Observed)
					}
					t.Logf("DIAGC3 %s attach stderr always dumped via SLICC_TEST_DUMP_ATTACH_STDERR; wire/hold via SLICC_TEST_DIAGC3_TRACE", name)
				}
			})
		}
	}
}
