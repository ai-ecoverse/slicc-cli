package integration

import (
	"testing"
)

func TestCDPC3TabAttachSettleDiag(t *testing.T) {
	const id = "C3.tab-attach"
	t.Setenv("SLICC_TEST_DUMP_ATTACH_STDERR", "1")
	t.Setenv("SLICC_DEBUG", "1")
	t.Setenv("SLICC_TEST_DIAGC3_TRACE", "1")
	t.Log("DIAGC3 memo A=skip empty-URL hold for page under tab; B=hold+resume at hold even waiting=false; C=announce held page child to client, admit only Runtime.runIfWaitingForDebugger until classification.")
	t.Log("DIAGC3 memo control wire notes: DIAGC3 control Target.attachedToTarget/targetInfoChanged with waiting; compare chrome->guard waiting vs guard->client.")
	t.Log("DIAGC3 memo pre-#850: watch guard->chrome Fetch.enable/Page.enable/setAutoAttach/runIfWaitingForDebugger on popup tab+page sessions and chrome events until hold expiry.")
	for _, holdVariant := range []string{"a", "b", "c"} {
		holdVariant := holdVariant
		for _, variant := range []string{"prompt", "lagging"} {
			variant := variant
			t.Run(holdVariant+"/"+variant, func(t *testing.T) {
				t.Setenv("SLICC_DIAGC3_VARIANT", holdVariant)
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
					t.Logf("DIAGC3 holdVariant=%s row control=%s guarded=%s live=%s why=%s", holdVariant, r.Control, r.Guarded, r.Live, r.Why)
					for _, err := range r.Errors {
						t.Logf("DIAGC3 %s error: %s", name, err)
					}
					for _, note := range r.Notes {
						t.Logf("DIAGC3 %s note: %s", name, note)
					}
					if r.Observed != nil {
						t.Logf("DIAGC3 %s observed=%s", name, r.Observed)
					}
				}
			})
		}
	}
}
