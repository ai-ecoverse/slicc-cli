package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hsOverlay(t *testing.T, scratch string, replacements map[string]string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(scratch, "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	return overlay
}

func TestCDPHistorySetupEdges(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	overlay := hsOverlay(t, t.TempDir(), map[string]string{
		filepath.Join(root, "internal/caps/cdp/history_setup_edges_test.go"): filepath.Join(root, "test/integration/testdata/historysetup/regression.go"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", "^TestHistorySetup", "./internal/caps/cdp")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("history setup edges: %v\n%s", err, output)
	}
}

type hsMutation struct {
	name    string
	file    string
	from    string
	to      string
	run     string
	failing string
}

func hsMutations() []hsMutation {
	return []hsMutation{
		{
			name:    "attached-check-removed",
			from:    " || !nt.done || nt.failed {\n",
			to:      " {\n",
			run:     "^TestHistorySetupShadowedConditions$/^not-done$/^bypass$",
			failing: "TestHistorySetupShadowedConditions/not-done/bypass",
		},
		{
			name:    "failed-check-removed",
			from:    " || !nt.done || nt.failed {\n",
			to:      " || !nt.done {\n",
			run:     "^TestHistorySetupShadowedConditions$/^failed$/^bypass$",
			failing: "TestHistorySetupShadowedConditions/failed/bypass",
		},
		{
			name:    "confirm-open-check-removed",
			from:    "\tif nt.confirm != nil && (!nt.confirm.done || nt.confirm.failed) {\n\t\treturn false\n",
			to:      "\tif nt.confirm != nil && nt.confirm.failed {\n\t\treturn false\n",
			run:     "^TestHistorySetupShadowedConditions$/^confirm-open$/^autoattach$",
			failing: "TestHistorySetupShadowedConditions/confirm-open/autoattach",
		},
		{
			name:    "confirm-failed-check-removed",
			from:    "\tif nt.confirm != nil && (!nt.confirm.done || nt.confirm.failed) {\n\t\treturn false\n",
			to:      "\tif nt.confirm != nil && !nt.confirm.done {\n\t\treturn false\n",
			run:     "^TestHistorySetupShadowedConditions$/^confirm-failed$/^bypass$",
			failing: "TestHistorySetupShadowedConditions/confirm-failed/bypass",
		},
		{
			name:    "error-page-check-removed",
			from:    "\tif nt.errorURL != \"\" || g.errorPageSessionLocked(c.session) {\n\t\treturn false\n\t}\n",
			to:      "",
			run:     "^TestHistorySetupShadowedConditions$/^error-url$/^autoattach$",
			failing: "TestHistorySetupShadowedConditions/error-url/autoattach",
		},
		{
			name:    "error-url-check-removed",
			from:    "\tif nt.errorURL != \"\" || g.errorPageSessionLocked(c.session) {\n",
			to:      "\tif g.errorPageSessionLocked(c.session) {\n",
			run:     "^TestHistorySetupConditions$/^ordinary-error-page$/^bypass$",
			failing: "TestHistorySetupConditions/ordinary-error-page/bypass",
		},
		{
			name:    "latch-check-removed",
			from:    "\treturn !nt.setupProtectedSeen\n",
			to:      "\treturn true\n",
			run:     "^TestHistorySetupProtectedSightingLatch$/^in-flight$/^bypass$",
			failing: "TestHistorySetupProtectedSightingLatch/in-flight/bypass",
		},
		{
			name:    "nav-latch-removed",
			from:    "\tif g.o.Loaded(nt.documentURL) || g.o.Loaded(nt.securityOrigin) || g.o.Loaded(nt.navigationURL) || (nt.errorURL != \"\" && g.o.Loaded(nt.errorURL)) {\n\t\tg.noteSetupProtectedLocked(nt)\n\t\treturn\n\t}\n",
			to:      "",
			run:     "^TestHistorySetupNavEventLatchPaths$/^non-fetch/frameStartedNavigating$",
			failing: "TestHistorySetupNavEventLatchPaths/non-fetch/frameStartedNavigating",
		},
		{
			name:    "fetch-document-latch-removed",
			from:    "\tif p.ResourceType == \"Document\" && nt.documentLocks(p.FrameID) && g.o.Loaded(p.Request.URL) {\n\t\tg.noteSetupProtectedLocked(nt)\n\t}\n",
			to:      "",
			run:     "^TestHistorySetupNavEventLatchPaths$/^fetch/requestPaused-Document$",
			failing: "TestHistorySetupNavEventLatchPaths/fetch/requestPaused-Document",
		},
		{
			name:    "fetch-unparseable-latch-removed",
			from:    "\tif json.Unmarshal(m.Params, &p) != nil {\n\t\tg.noteSetupProtectedLocked(nt)\n\t\treturn\n\t}\n",
			to:      "\tif json.Unmarshal(m.Params, &p) != nil {\n\t\treturn\n\t}\n",
			run:     "^TestHistorySetupNavEventLatchPaths$/^fetch/requestPaused-unparseable$",
			failing: "TestHistorySetupNavEventLatchPaths/fetch/requestPaused-unparseable",
		},
		{
			name:    "observe-latch-removed",
			from:    "\tif v.unparseable || g.o.Loaded(t.url) {\n\t\tg.noteSetupProtectedLocked(nt)\n\t}\n",
			to:      "",
			run:     "^TestHistorySetupNavEventLatchPaths$/^observe/targetInfoChanged$",
			failing: "TestHistorySetupNavEventLatchPaths/observe/targetInfoChanged",
		},
		{
			name:    "initial-info-latch-removed",
			from:    "\t\tif infoBad || g.o.Loaded(url) {\n\t\t\treturn g.settleInitialProtectedLocked(nt)\n\t\t}\n",
			to:      "\t\tif infoBad || g.o.Loaded(url) {\n\t\t\tnt.historyLocked = true\n\t\t\tnt.settle(false)\n\t\t\treturn nil\n\t\t}\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^protected-current-info$",
			failing: "TestHistorySetupInitialLatchDirections/protected-current-info",
		},
		{
			name:    "initial-frame-latch-removed",
			from:    "\tcase historyProtected:\n\t\treturn g.settleInitialProtectedLocked(nt)\n\tcase historyUnknown:\n\t\tif !chromeErrFresh {\n\t\t\treturn g.deferInitialLocked(nt)\n\t\t}\n",
			to:      "\tcase historyProtected:\n\t\tnt.historyLocked = true\n\t\tnt.settle(false)\n\t\treturn nil\n\tcase historyUnknown:\n\t\tif !chromeErrFresh {\n\t\t\treturn g.deferInitialLocked(nt)\n\t\t}\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^protected-current-frame$",
			failing: "TestHistorySetupInitialLatchDirections/protected-current-frame",
		},
		{
			name:    "current-protected-latch-removed",
			from:    "\t\tif currentProtected {\n\t\t\tnt.historyLocked = true\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t}\n",
			to:      "\t\tif currentProtected {\n\t\t\tnt.historyLocked = true\n\t\t}\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^protected-currentIndex-latches$",
			failing: "TestHistorySetupInitialLatchDirections/protected-currentIndex-latches",
		},
		{
			name:    "chrome-error-frame-unknown-removed",
			from:    "\tif f.ID != target || f.URL == nil || *f.URL == \"\" || f.SecurityOrigin == nil || *f.SecurityOrigin == \"\" || chromeErrorDocument(*f.URL) {\n\t\treturn historyUnknown\n\t}\n",
			to:      "\tif f.ID != target || f.URL == nil || *f.URL == \"\" || f.SecurityOrigin == nil || *f.SecurityOrigin == \"\" {\n\t\treturn historyUnknown\n\t}\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^unknown-history-chrome-error-frame-locks$",
			failing: "TestHistorySetupInitialLatchDirections/unknown-history-chrome-error-frame-locks",
		},
		{
			name:    "clean-inheriting-extra-read-restored",
			from:    "\t\tif !nt.historyLocked && state == historyClean && url != \"\" {\n\t\t\tnt.settle(false)\n\t\t\treturn nil\n\t\t}\n",
			to:      "\t\tincomplete := protect.Inheriting(url)\n\t\tif !nt.historyLocked && !incomplete && state == historyClean {\n\t\t\tnt.settle(false)\n\t\t\treturn nil\n\t\t}\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^clean-about-blank-settles-without-frame$",
			failing: "TestHistorySetupInitialLatchDirections/clean-about-blank-settles-without-frame",
		},
		{
			name:    "chrome-error-info-locks-clean",
			from:    "\t\tif chromeErrorDocument(url) && (nt.historyLocked || state == historyClean) {\n\t\t\tif nt.historyLocked {\n\t\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t\t}\n\t\t\tnt.settle(false)\n\t\t\treturn nil\n\t\t}\n",
			to:      "\t\tif chromeErrorDocument(url) && (nt.historyLocked || state == historyClean) {\n\t\t\treturn g.settleInitialProtectedLocked(nt)\n\t\t}\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^clean-chrome-error-info-admits$",
			failing: "TestHistorySetupInitialLatchDirections/clean-chrome-error-info-admits",
		},
		{
			name:    "chrome-error-info-ignores-unknown-history",
			from:    "\t\tif chromeErrorDocument(url) && (nt.historyLocked || state == historyClean) {\n\t\t\tif nt.historyLocked {\n\t\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t\t}\n\t\t\tnt.settle(false)\n\t\t\treturn nil\n\t\t}\n",
			to:      "\t\tif chromeErrorDocument(url) {\n\t\t\tif nt.historyLocked {\n\t\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t\t}\n\t\t\tnt.settle(false)\n\t\t\treturn nil\n\t\t}\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^unknown-history-chrome-error-info-locks$",
			failing: "TestHistorySetupInitialLatchDirections/unknown-history-chrome-error-info-locks",
		},
		{
			name:    "chrome-error-defer-restored",
			from:    "\t\tif chromeErrorDocument(url) && (nt.historyLocked || state == historyClean) {\n\t\t\tif nt.historyLocked {\n\t\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t\t}\n\t\t\tnt.settle(false)\n\t\t\treturn nil\n\t\t}\n",
			to:      "",
			run:     "^TestHistorySetupInitialLatchDirections$/^ordinary-error-page-before-attach-latches$",
			failing: "TestHistorySetupInitialLatchDirections/ordinary-error-page-before-attach-latches",
		},
		{
			name:    "chrome-error-frame-locks-clean",
			from:    "\tchromeErrFresh := g.chromeErrorFrameMessage(frame) && (nt.historyLocked || hist == historyClean)\n\tswitch g.initialFrameStatus(frame, nt.target) {\n\tcase historyProtected:\n\t\treturn g.settleInitialProtectedLocked(nt)\n\tcase historyUnknown:\n\t\tif !chromeErrFresh {\n\t\t\treturn g.deferInitialLocked(nt)\n\t\t}\n\t}\n\tif nt.historyLocked {\n\t\tif chromeErrFresh {\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t}\n\t\tnt.settle(false)\n\t\treturn nil\n\t}\n\treturn [][]byte{g.callbackLocked(nt.session, \"Page.getNavigationHistory\", nil, func(fresh message) [][]byte {\n\t\tg.rememberHistoryRefusals(nt, fresh)\n\t\tswitch g.historyStatus(fresh) {\n\t\tcase historyProtected:\n\t\t\tnt.historyLocked = true\n\t\tcase historyUnknown:\n\t\t\tif !nt.historyLocked {\n\t\t\t\treturn g.deferInitialLocked(nt)\n\t\t\t}\n\t\t}\n\t\tif chromeErrFresh && nt.historyLocked {\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t}\n\t\tnt.settle(false)\n\t\treturn nil\n\t})}\n",
			to:      "\tchromeErrFresh := g.chromeErrorFrameMessage(frame) && (nt.historyLocked || hist == historyClean)\n\tswitch g.initialFrameStatus(frame, nt.target) {\n\tcase historyProtected:\n\t\treturn g.settleInitialProtectedLocked(nt)\n\tcase historyUnknown:\n\t\tif !chromeErrFresh {\n\t\t\treturn g.deferInitialLocked(nt)\n\t\t}\n\t\treturn g.settleInitialProtectedLocked(nt)\n\t}\n\tif nt.historyLocked {\n\t\tif chromeErrFresh {\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t}\n\t\tnt.settle(false)\n\t\treturn nil\n\t}\n\treturn [][]byte{g.callbackLocked(nt.session, \"Page.getNavigationHistory\", nil, func(fresh message) [][]byte {\n\t\tg.rememberHistoryRefusals(nt, fresh)\n\t\tswitch g.historyStatus(fresh) {\n\t\tcase historyProtected:\n\t\t\tnt.historyLocked = true\n\t\tcase historyUnknown:\n\t\t\tif !nt.historyLocked {\n\t\t\t\treturn g.deferInitialLocked(nt)\n\t\t\t}\n\t\t}\n\t\tif chromeErrFresh && nt.historyLocked {\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t}\n\t\tnt.settle(false)\n\t\treturn nil\n\t})}\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^clean-incomplete-chrome-error-frame-admits$",
			failing: "TestHistorySetupInitialLatchDirections/clean-incomplete-chrome-error-frame-admits",
		},
		{
			name:    "chrome-error-frame-ignores-unknown-history",
			from:    "\tchromeErrFresh := g.chromeErrorFrameMessage(frame) && (nt.historyLocked || hist == historyClean)\n",
			to:      "\tchromeErrFresh := g.chromeErrorFrameMessage(frame)\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^unknown-history-chrome-error-frame-locks$",
			failing: "TestHistorySetupInitialLatchDirections/unknown-history-chrome-error-frame-locks",
		},
		{
			name:    "chrome-error-frame-settles-without-fresh",
			from:    "\tchromeErrFresh := g.chromeErrorFrameMessage(frame) && (nt.historyLocked || hist == historyClean)\n\tswitch g.initialFrameStatus(frame, nt.target) {\n\tcase historyProtected:\n\t\treturn g.settleInitialProtectedLocked(nt)\n\tcase historyUnknown:\n\t\tif !chromeErrFresh {\n\t\t\treturn g.deferInitialLocked(nt)\n\t\t}\n\t}\n\tif nt.historyLocked {\n\t\tif chromeErrFresh {\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t}\n\t\tnt.settle(false)\n\t\treturn nil\n\t}\n\treturn [][]byte{g.callbackLocked(nt.session, \"Page.getNavigationHistory\", nil, func(fresh message) [][]byte {\n\t\tg.rememberHistoryRefusals(nt, fresh)\n\t\tswitch g.historyStatus(fresh) {\n\t\tcase historyProtected:\n\t\t\tnt.historyLocked = true\n\t\tcase historyUnknown:\n\t\t\tif !nt.historyLocked {\n\t\t\t\treturn g.deferInitialLocked(nt)\n\t\t\t}\n\t\t}\n\t\tif chromeErrFresh && nt.historyLocked {\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t}\n\t\tnt.settle(false)\n\t\treturn nil\n\t})}\n",
			to:      "\tswitch g.initialFrameStatus(frame, nt.target) {\n\tcase historyProtected:\n\t\treturn g.settleInitialProtectedLocked(nt)\n\tcase historyUnknown:\n\t\tif g.chromeErrorFrameMessage(frame) && (nt.historyLocked || hist == historyClean) {\n\t\t\tif nt.historyLocked {\n\t\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t\t}\n\t\t\tnt.settle(false)\n\t\t\treturn nil\n\t\t}\n\t\treturn g.deferInitialLocked(nt)\n\t}\n\treturn [][]byte{g.callbackLocked(nt.session, \"Page.getNavigationHistory\", nil, func(fresh message) [][]byte {\n\t\tg.rememberHistoryRefusals(nt, fresh)\n\t\tswitch g.historyStatus(fresh) {\n\t\tcase historyProtected:\n\t\t\tnt.historyLocked = true\n\t\tcase historyUnknown:\n\t\t\tif !nt.historyLocked {\n\t\t\t\treturn g.deferInitialLocked(nt)\n\t\t\t}\n\t\t}\n\t\tnt.settle(false)\n\t\treturn nil\n\t})}\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^clean-then-protected-chrome-error-frame-locks$",
			failing: "TestHistorySetupInitialLatchDirections/clean-then-protected-chrome-error-frame-locks",
		},
		{
			name:    "chrome-error-frame-settle-removed",
			from:    "\tchromeErrFresh := g.chromeErrorFrameMessage(frame) && (nt.historyLocked || hist == historyClean)\n\tswitch g.initialFrameStatus(frame, nt.target) {\n\tcase historyProtected:\n\t\treturn g.settleInitialProtectedLocked(nt)\n\tcase historyUnknown:\n\t\tif !chromeErrFresh {\n\t\t\treturn g.deferInitialLocked(nt)\n\t\t}\n\t}\n\tif nt.historyLocked {\n\t\tif chromeErrFresh {\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t}\n\t\tnt.settle(false)\n\t\treturn nil\n\t}\n",
			to:      "\tchromeErrFresh := false\n\tswitch g.initialFrameStatus(frame, nt.target) {\n\tcase historyProtected:\n\t\treturn g.settleInitialProtectedLocked(nt)\n\tcase historyUnknown:\n\t\tif !chromeErrFresh {\n\t\t\treturn g.deferInitialLocked(nt)\n\t\t}\n\t}\n\tif nt.historyLocked {\n\t\tif chromeErrFresh {\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t}\n\t\tnt.settle(false)\n\t\treturn nil\n\t}\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^ordinary-url-chrome-error-frame-settles$",
			failing: "TestHistorySetupInitialLatchDirections/ordinary-url-chrome-error-frame-settles",
		},
		{
			name:    "locked-frame-second-history-restored",
			from:    "\tif nt.historyLocked {\n\t\tif chromeErrFresh {\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t}\n\t\tnt.settle(false)\n\t\treturn nil\n\t}\n",
			to:      "",
			run:     "^TestHistorySetupInitialLatchDirections$/^locked-frame-one-history$",
			failing: "TestHistorySetupInitialLatchDirections/locked-frame-one-history",
		},
		{
			name:    "defer-unknown-latch-restored",
			from:    "\t\tif !chromeErrFresh {\n\t\t\treturn g.deferInitialLocked(nt)\n\t\t}\n\t}\n\tif nt.historyLocked {\n\t\tif chromeErrFresh {\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t}\n\t\tnt.settle(false)\n\t\treturn nil\n\t}\n\treturn [][]byte{g.callbackLocked(nt.session, \"Page.getNavigationHistory\", nil, func(fresh message) [][]byte {\n",
			to:      "\t\tif !chromeErrFresh {\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t\treturn g.deferInitialLocked(nt)\n\t\t}\n\t}\n\tif nt.historyLocked {\n\t\tif chromeErrFresh {\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t}\n\t\tnt.settle(false)\n\t\treturn nil\n\t}\n\treturn [][]byte{g.callbackLocked(nt.session, \"Page.getNavigationHistory\", nil, func(fresh message) [][]byte {\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^unknown-then-clean-retry-admits$",
			failing: "TestHistorySetupInitialLatchDirections/unknown-then-clean-retry-admits",
		},
		{
			name:    "initial-unknown-bound-removed",
			from:    "\tif nt.historyLocked {\n\t\tnt.initialUnknowns++\n\t\tif nt.initialUnknowns >= maxInitialUnknowns {\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\t\tnt.settle(false)\n\t\t\treturn nil\n\t\t}\n\t\tif g.scheduleInitial != nil {\n\t\t\tg.scheduleInitial(nt)\n\t\t\treturn nil\n\t\t}\n\t\tg.noteSetupProtectedLocked(nt)\n\t\tnt.settle(false)\n\t\treturn nil\n\t}\n",
			to:      "\tif nt.historyLocked {\n\t\tif g.scheduleInitial != nil {\n\t\t\tg.scheduleInitial(nt)\n\t\t\treturn nil\n\t\t}\n\t\tg.noteSetupProtectedLocked(nt)\n\t\tnt.settle(false)\n\t\treturn nil\n\t}\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^locked-unknown-frame-bound-settles$",
			failing: "TestHistorySetupInitialLatchDirections/locked-unknown-frame-bound-settles",
		},
		{
			name:    "locked-retry-rereads-history",
			from:    "\tif nt.historyLocked {\n\t\treturn g.callbackLocked(nt.session, \"Page.getFrameTree\", nil, func(frame message) [][]byte {\n\t\t\treturn g.initialFrameTreeLocked(nt, frame, historyUnknown)\n\t\t})\n\t}\n",
			to:      "",
			run:     "^TestHistorySetupInitialLatchDirections$/^locked-unknown-frame-one-history$",
			failing: "TestHistorySetupInitialLatchDirections/locked-unknown-frame-one-history",
		},
		{
			name:    "defer-final-unlocks",
			from:    "\tnt.historyLocked = true\n\tg.noteSetupProtectedLocked(nt)\n\tnt.settle(false)\n\treturn nil\n}\n\nfunc (g *guard) retryInitial(nt *navTarget) []byte {\n",
			to:      "\tnt.settle(false)\n\treturn nil\n}\n\nfunc (g *guard) retryInitial(nt *navTarget) []byte {\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^no-scheduler-unknown-locks$",
			failing: "TestHistorySetupInitialLatchDirections/no-scheduler-unknown-locks",
		},
		{
			name:    "second-callback-protected-latches",
			from:    "\t\tcase historyProtected:\n\t\t\tnt.historyLocked = true\n\t\tcase historyUnknown:\n\t\t\tif !nt.historyLocked {\n\t\t\t\treturn g.deferInitialLocked(nt)\n\t\t\t}\n",
			to:      "\t\tcase historyProtected:\n\t\t\tnt.historyLocked = true\n\t\t\tg.noteSetupProtectedLocked(nt)\n\t\tcase historyUnknown:\n\t\t\tif !nt.historyLocked {\n\t\t\t\treturn g.deferInitialLocked(nt)\n\t\t\t}\n",
			run:     "^TestHistorySetupInitialLatchDirections$/^historyProtected-second-callback-no-latch$",
			failing: "TestHistorySetupInitialLatchDirections/historyProtected-second-callback-no-latch",
		},
		{
			name:    "stale-clear-allowed",
			from:    "\tif issuedSeq <= nt.setupProtectedSeq {\n\t\treturn\n\t}\n",
			to:      "",
			run:     "^TestHistorySetupStaleConfirmDoesNotClearLatch$",
			failing: "TestHistorySetupStaleConfirmDoesNotClearLatch",
		},
		{
			name:    "clear-pending-nav-removed",
			from:    "\tif len(nt.loading) != 0 || nt.unsure {\n\t\treturn\n\t}\n",
			to:      "",
			run:     "^TestHistorySetupPendingNavConfirmDoesNotClearLatch$",
			failing: "TestHistorySetupPendingNavConfirmDoesNotClearLatch",
		},
		{
			name:    "clear-protected-state-removed",
			from:    "\tif g.o.Loaded(nt.navigationURL) || g.o.Loaded(nt.documentURL) || g.o.Loaded(nt.securityOrigin) || (nt.errorURL != \"\" && g.o.Loaded(nt.errorURL)) {\n\t\treturn\n\t}\n",
			to:      "",
			run:     "^TestHistorySetupProtectedNavURLDoesNotClearLatch$",
			failing: "TestHistorySetupProtectedNavURLDoesNotClearLatch",
		},
		{
			name:    "clear-on-ordinary-removed",
			from:    "\tnt.setupProtectedSeen = false\n\tnt.setupProtectedSeq = 0\n",
			to:      "",
			run:     "^TestHistorySetupLatchClearedByOrdinaryConfirm$",
			failing: "TestHistorySetupLatchClearedByOrdinaryConfirm",
		},
		{
			name:    "confirm-top-url-latch-removed",
			from:    "\tif t != nil && g.o.Loaded(t.url) {\n\t\tg.noteSetupProtectedLocked(nt)\n\t\treturn\n\t}\n",
			to:      "",
			run:     "^TestHistorySetupConfirmTopTargetURLLatch$",
			failing: "TestHistorySetupConfirmTopTargetURLLatch",
		},
		{
			name:    "inherited-empty-origin-clears",
			from:    "\tif frame.URL == \"\" || protect.Unparseable(frame.URL) || frame.SecurityOrigin == \"\" {\n\t\tg.noteSetupProtectedLocked(nt)\n\t}\n",
			to:      "\tif frame.URL == \"\" || protect.Unparseable(frame.URL) || (frame.SecurityOrigin == \"\" && !inheritedDocument(frame.URL, frame.UnreachableURL)) {\n\t\tg.noteSetupProtectedLocked(nt)\n\t}\n",
			run:     "^TestHistorySetupInheritedEmptyOriginKeepsLatch$",
			failing: "TestHistorySetupInheritedEmptyOriginKeepsLatch",
		},
		{
			name:    "inherited-clear-site-removed",
			from:    "\tif inheritedDocument(frame.URL, frame.UnreachableURL) {\n\t\tif g.o.Target(frame.URL, t.protected, g.openerState(t)) || !g.ordinaryFrameParentLocked(t, frame.ParentID) {\n\t\t\tcheck.reason = g.frameParentReasonLocked(t, frame.ParentID)\n\t\t\tcheck.settle(true)\n\t\t\treturn g.releaseHistoryHoldsForConfirmLocked(nt.target, false, issuedSeq)\n\t\t}\n\t\tcheck.settle(false)\n\t\tg.stampHistoryHoldReadIdentityLocked(nt.target, frame)\n\t\tg.clearSetupProtectedLocked(nt, issuedSeq)\n\t\treturn g.releaseHistoryHoldsForConfirmLocked(nt.target, true, issuedSeq)\n\t}\n",
			to:      "\tif inheritedDocument(frame.URL, frame.UnreachableURL) {\n\t\tif g.o.Target(frame.URL, t.protected, g.openerState(t)) || !g.ordinaryFrameParentLocked(t, frame.ParentID) {\n\t\t\tcheck.reason = g.frameParentReasonLocked(t, frame.ParentID)\n\t\t\tcheck.settle(true)\n\t\t\treturn g.releaseHistoryHoldsForConfirmLocked(nt.target, false, issuedSeq)\n\t\t}\n\t\tcheck.settle(false)\n\t\tg.stampHistoryHoldReadIdentityLocked(nt.target, frame)\n\t\treturn g.releaseHistoryHoldsForConfirmLocked(nt.target, true, issuedSeq)\n\t}\n",
			file:    "internal/caps/cdp/targetguard.go",
			run:     "^TestHistorySetupClearSites$/^inherited-branch$",
			failing: "TestHistorySetupClearSites/inherited-branch",
		},
		{
			name:    "final-clear-site-removed",
			from:    "\tcheck.settle(false)\n\tg.stampHistoryHoldReadIdentityLocked(nt.target, frame)\n\tg.clearSetupProtectedLocked(nt, issuedSeq)\n\treturn g.releaseHistoryHoldsForConfirmLocked(nt.target, true, issuedSeq)\n",
			to:      "\tcheck.settle(false)\n\tg.stampHistoryHoldReadIdentityLocked(nt.target, frame)\n\treturn g.releaseHistoryHoldsForConfirmLocked(nt.target, true, issuedSeq)\n",
			file:    "internal/caps/cdp/targetguard.go",
			run:     "^TestHistorySetupClearSites$/^final-branch$",
			failing: "TestHistorySetupClearSites/final-branch",
		},
		{
			name:    "lock-frames-latch-removed",
			from:    "\tnt.historyLocked = true\n\tg.noteSetupProtectedLocked(nt)\n",
			to:      "\tnt.historyLocked = true\n",
			file:    "internal/caps/cdp/documentsonly.go",
			run:     "^TestHistorySetupDocumentsOnlyLockLatches$",
			failing: "TestHistorySetupDocumentsOnlyLockLatches",
		},
		{
			name:    "admit-switch-removed",
			from:    "\tif !historySetupAdmitEnabled() {\n\t\treturn false\n\t}\n",
			to:      "\tif historySetupAdmitEnabled() {\n\t\treturn false\n\t}\n",
			run:     "^TestHistorySetupAdmittedOnLockedTab$/^bypass$",
			failing: "TestHistorySetupAdmittedOnLockedTab/bypass",
		},
		{
			name:    "bypass-value-unchecked",
			from:    "\t\treturn c.params[\"bypass\"] == true\n",
			to:      "\t\treturn true\n",
			run:     "^TestHistorySetupParamsRefused$/^bypass-false$",
			failing: "TestHistorySetupParamsRefused/bypass-false",
		},
		{
			name:    "bypass-type-unchecked",
			from:    "\t\treturn c.params[\"bypass\"] == true\n",
			to:      "\t\treturn c.params[\"bypass\"] != false\n",
			run:     "^TestHistorySetupParamsRefused$/^bypass-string$",
			failing: "TestHistorySetupParamsRefused/bypass-string",
		},
		{
			name:    "extra-keys-allowed",
			from:    "\t\tif !keys[key] {\n\t\t\treturn false\n\t\t}\n",
			to:      "\t\t_ = keys[key]\n",
			run:     "^TestHistorySetupParamsRefused$/^bypass-extra-key$",
			failing: "TestHistorySetupParamsRefused/bypass-extra-key",
		},
		{
			name:    "autoattach-values-unchecked",
			from:    "\treturn c.params[\"autoAttach\"] == true && c.params[\"flatten\"] == true && ",
			to:      "\treturn ",
			run:     "^TestHistorySetupParamsRefused$/^autoattach-off$",
			failing: "TestHistorySetupParamsRefused/autoattach-off",
		},
		{
			name:    "flatten-unchecked",
			from:    " && c.params[\"flatten\"] == true && ",
			to:      " && ",
			run:     "^TestHistorySetupParamsRefused$/^autoattach-flatten-false$",
			failing: "TestHistorySetupParamsRefused/autoattach-flatten-false",
		},
		{
			name:    "wait-unchecked",
			from:    " && (!waitNamed || wait == false) && ",
			to:      " && (!waitNamed || wait != nil) && ",
			run:     "^TestHistorySetupParamsRefused$/^autoattach-wait-true$",
			failing: "TestHistorySetupParamsRefused/autoattach-wait-true",
		},
		{
			name:    "filter-unchecked",
			from:    "&& filterNamed && reflect.DeepEqual(filter, historySetupFilter)",
			to:      "&& (!filterNamed || reflect.DeepEqual(filter, historySetupFilter))",
			run:     "^TestHistorySetupParamsRefused$/^autoattach-no-filter$",
			failing: "TestHistorySetupParamsRefused/autoattach-no-filter",
		},
		{
			name:    "methods-widened-to-target",
			from:    "\tkeys, ok := historySetupKeys[c.method]\n",
			to:      "\tif strings.HasPrefix(c.method, \"Target.\") {\n\t\treturn true\n\t}\n\tkeys, ok := historySetupKeys[c.method]\n",
			run:     "^TestHistorySetupParamsRefused$/^set-discover$",
			failing: "TestHistorySetupParamsRefused/set-discover",
		},
		{
			name:    "methods-widened-to-emulation",
			from:    "\tkeys, ok := historySetupKeys[c.method]\n",
			to:      "\tif strings.HasPrefix(c.method, \"Emulation.\") {\n\t\treturn true\n\t}\n\tkeys, ok := historySetupKeys[c.method]\n",
			run:     "^TestHistorySetupParamsRefused$/^focus$",
			failing: "TestHistorySetupParamsRefused/focus",
		},
		{
			name:    "methods-widened-to-document-reads",
			from:    "\tkeys, ok := historySetupKeys[c.method]\n",
			to:      "\tif c.method == \"DOM.getDocument\" {\n\t\treturn true\n\t}\n\tkeys, ok := historySetupKeys[c.method]\n",
			run:     "^TestHistorySetupContentStaysRefused$/^get-document-depth-0$",
			failing: "TestHistorySetupContentStaysRefused/get-document-depth-0",
		},
		{
			name:    "probe-switch-removed",
			from:    "\tif (c.method == \"DOM.getDocument\" || c.method == \"DOM.disable\") && !historySetupProbeAdmitEnabled() {\n\t\treturn false\n\t}\n",
			to:      "\tif (c.method == \"DOM.getDocument\" || c.method == \"DOM.disable\") && historySetupProbeAdmitEnabled() {\n\t\treturn false\n\t}\n",
			run:     "^TestHistorySetupProbeAdmittedOnLockedTab$",
			failing: "TestHistorySetupProbeAdmittedOnLockedTab",
		},
		{
			name:    "probe-depth-unchecked",
			from:    "\tif !historySetupDepthMinusOne(params) || params[\"pierce\"] != true {\n",
			to:      "\tif params[\"pierce\"] != true {\n",
			run:     "^TestHistorySetupParamsRefused$/^dom-get-document-depth-0$",
			failing: "TestHistorySetupParamsRefused/dom-get-document-depth-0",
		},
		{
			name:    "probe-pierce-unchecked",
			from:    "\tif !historySetupDepthMinusOne(params) || params[\"pierce\"] != true {\n",
			to:      "\tif !historySetupDepthMinusOne(params) {\n",
			run:     "^TestHistorySetupParamsRefused$/^dom-get-document-pierce-false$",
			failing: "TestHistorySetupParamsRefused/dom-get-document-pierce-false",
		},
		{
			name:    "disable-without-probe-admitted",
			from:    "\t\treturn g.historySetupProbe[c.session]\n",
			to:      "\t\treturn true\n",
			run:     "^TestHistorySetupDisableWithoutProbeRefused$",
			failing: "TestHistorySetupDisableWithoutProbeRefused",
		},
		{
			name:    "disable-blocked-after-protect",
			from:    "\tif g.historySetupDisableLocked(c, s) {\n\t\treturn true\n\t}\n",
			to:      "",
			run:     "^TestHistorySetupDisableAfterProtectAdmitted$",
			failing: "TestHistorySetupDisableAfterProtectAdmitted",
		},
		{
			name:    "history-locked-events-narrowed-to-list",
			from:    "\tif strings.HasPrefix(m.Method, \"DOM.\") {\n",
			to:      "\tif false && strings.HasPrefix(m.Method, \"DOM.\") {\n",
			file:    "internal/caps/cdp/paint.go",
			run:     "^TestHistorySetupProbeEventsDropped$",
			failing: "TestHistorySetupProbeEventsDropped",
		},
		{
			name:    "probe-identity-hold-bypassed",
			from:    "\tif !ok || m == nil || m.ID == nil {\n\t\treturn nil, nil, false\n\t}\n\tif g.relaySession == \"\" {\n",
			to:      "\tif !ok || m == nil || m.ID == nil {\n\t\treturn nil, nil, false\n\t}\n\treturn nil, nil, false\n\tif g.relaySession == \"\" {\n",
			file:    "internal/caps/cdp/documentidentity.go",
			run:     "^TestHistorySetupProbeIdentityTaintRace$",
			failing: "TestHistorySetupProbeIdentityTaintRace",
		},
		{
			name:    "probe-child-doc-strip-disabled",
			from:    "func childDocStripEnabled() bool {\n\treturn true\n}",
			to:      "func childDocStripEnabled() bool {\n\treturn false\n}",
			file:    "internal/caps/cdp/childdoc_switches.go",
			run:     "^TestHistorySetupProbeProtectedChildRefused$",
			failing: "TestHistorySetupProbeProtectedChildRefused",
		},
		{
			name:    "probe-oopif-src-scrub-disabled",
			from:    "func (g *guard) cutEmbeddedKind(s, kind string) (string, bool) {\n\tif !strings.ContainsAny(s, \"/%\") || !strings.Contains(s, \"%\") && !strings.Contains(s, \"://\") && !g.o.MayNameHost(s) && !g.hasProtectedIP() {\n\t\treturn s, false\n\t}\n",
			to:      "func (g *guard) cutEmbeddedKind(s, kind string) (string, bool) {\n\treturn s, false\n\tif !strings.ContainsAny(s, \"/%\") || !strings.Contains(s, \"%\") && !strings.Contains(s, \"://\") && !g.o.MayNameHost(s) && !g.hasProtectedIP() {\n\t\treturn s, false\n\t}\n",
			file:    "internal/caps/cdp/scrub.go",
			run:     "^TestHistorySetupProbeOOPIFSrcScrubbed$",
			failing: "TestHistorySetupProbeOOPIFSrcScrubbed",
		},
		{
			name:    "disable-latch-map-unchecked",
			from:    "\tfor key := range c.params {\n\t\tif !historySetupKeys[\"DOM.disable\"][key] {\n\t\t\treturn false\n\t\t}\n\t}\n\treturn g.historySetupProbe[c.session]\n",
			to:      "\tfor key := range c.params {\n\t\tif !historySetupKeys[\"DOM.disable\"][key] {\n\t\t\treturn false\n\t\t}\n\t}\n\treturn true\n",
			run:     "^TestHistorySetupDisableWithoutProbeRefused$",
			failing: "TestHistorySetupDisableWithoutProbeRefused",
		},
		{
			name:    "probe-latch-writer-skips-history-lock",
			from:    "\t\tif nt == nil || !nt.historyLocked || !historySetupProbeAdmitEnabled() || !historySetupProbeParams(c.params) {\n\t\t\treturn\n\t\t}\n",
			to:      "\t\tif nt == nil || !historySetupProbeAdmitEnabled() || !historySetupProbeParams(c.params) {\n\t\t\treturn\n\t\t}\n",
			run:     "^TestHistorySetupProbeLatchRequiresHistoryLock$",
			failing: "TestHistorySetupProbeLatchRequiresHistoryLock",
		},
		{
			name:    "probe-latch-writer-switch-forced-off",
			from:    "\t\tif nt == nil || !nt.historyLocked || !historySetupProbeAdmitEnabled() || !historySetupProbeParams(c.params) {\n\t\t\treturn\n\t\t}\n",
			to:      "\t\tif nt == nil || !nt.historyLocked || true || !historySetupProbeParams(c.params) {\n\t\t\treturn\n\t\t}\n",
			run:     "^TestHistorySetupProbeAdmittedOnLockedTab$",
			failing: "TestHistorySetupProbeAdmittedOnLockedTab",
		},
		{
			name:    "probe-latch-forget-removed",
			from:    "\tdelete(g.domEnabled, id)\n\tdelete(g.axEnabled, id)\n\tdelete(g.historySetupProbe, id)\n",
			to:      "\tdelete(g.domEnabled, id)\n\tdelete(g.axEnabled, id)\n",
			file:    "internal/caps/cdp/guard.go",
			run:     "^TestHistorySetupProbeLatchClearedOnForget$",
			failing: "TestHistorySetupProbeLatchClearedOnForget",
		},
		{
			name:    "probe-disable-switch-forced-off",
			from:    "\tif !historySetupProbeAdmitEnabled() || c.method != \"DOM.disable\" {\n\t\treturn false\n\t}\n",
			to:      "\tif true || c.method != \"DOM.disable\" {\n\t\treturn false\n\t}\n",
			run:     "^TestHistorySetupDisableAfterProtectAdmitted$",
			failing: "TestHistorySetupDisableAfterProtectAdmitted",
		},
		{
			name:    "page-sessions-only-removed",
			from:    "\tif s.typ != \"page\" || g.browserSessions[c.session] || ",
			to:      "\tif g.browserSessions[c.session] || ",
			run:     "^TestHistorySetupConditions$/^webview-target$/^bypass$",
			failing: "TestHistorySetupConditions/webview-target/bypass",
		},
		{
			name:    "browser-sessions-check-removed",
			from:    "\tif s.typ != \"page\" || g.browserSessions[c.session] || ",
			to:      "\tif s.typ != \"page\" || ",
			run:     "^TestHistorySetupShadowedConditions$/^browser-flagged-session$",
			failing: "TestHistorySetupShadowedConditions/browser-flagged-session",
		},
	}
}

func TestCDPHistorySetupHoldMutant(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	mutant := filepath.Join(scratch, "mutation_hold_removed.go")
	data, err := os.ReadFile(filepath.Join(root, "test/integration/testdata/historyrestore/mutation_hold_removed.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mutant, data, 0600); err != nil {
		t.Fatal(err)
	}
	overlay := hsOverlay(t, scratch, map[string]string{
		filepath.Join(root, "internal/caps/cdp/history_setup_edges_test.go"): filepath.Join(root, "test/integration/testdata/historysetup/regression.go"),
		filepath.Join(root, "internal/caps/cdp/history_restore_switches.go"): mutant,
	})
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", "^TestHistorySetupAutoAttachHold$", "./internal/caps/cdp")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("hold-removed mutant must fail AutoAttachHold\n%s", output)
	}
	if !strings.Contains(string(output), "TestHistorySetupAutoAttachHold") {
		t.Fatalf("mutant did not fail named row:\n%s", output)
	}
}

func TestCDPHistorySetupProbeRemovedMutant(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	mutant := filepath.Join(scratch, "mutation_probe_removed.go")
	data, err := os.ReadFile(filepath.Join(root, "test/integration/testdata/historysetup/mutation_probe_removed.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mutant, data, 0600); err != nil {
		t.Fatal(err)
	}
	overlay := hsOverlay(t, scratch, map[string]string{
		filepath.Join(root, "internal/caps/cdp/history_setup_edges_test.go"): filepath.Join(root, "test/integration/testdata/historysetup/regression.go"),
		filepath.Join(root, "internal/caps/cdp/history_setup_switches.go"):   mutant,
	})
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", "^TestHistorySetupProbeAdmittedOnLockedTab$", "./internal/caps/cdp")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("probe-removed mutant must fail ProbeAdmittedOnLockedTab\n%s", output)
	}
	if !strings.Contains(string(output), "--- FAIL: TestHistorySetupProbeAdmittedOnLockedTab ") {
		t.Fatalf("mutant did not fail named row:\n%s", output)
	}
}

func TestCDPHistorySetupMutations(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	regression := filepath.Join(root, "test/integration/testdata/historysetup/regression.go")
	for _, m := range hsMutations() {
		t.Run(m.name, func(t *testing.T) {
			rel := m.file
			if rel == "" {
				rel = "internal/caps/cdp/conformance.go"
			}
			src := filepath.Join(root, rel)
			raw, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			text := string(raw)
			if strings.Count(text, m.from) != 1 {
				t.Fatalf("mutation %s: anchor must occur once in %s:\n%s", m.name, rel, m.from)
			}
			scratch := t.TempDir()
			mutant := filepath.Join(scratch, "mutation_"+filepath.Base(rel))
			if err := os.WriteFile(mutant, []byte(strings.Replace(text, m.from, m.to, 1)), 0600); err != nil {
				t.Fatal(err)
			}
			overlay := hsOverlay(t, scratch, map[string]string{
				filepath.Join(root, "internal/caps/cdp/history_setup_edges_test.go"): regression,
				src: mutant,
			})
			ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", overlay, "-run", m.run, "./internal/caps/cdp")
			command.Dir = root
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("mutation %s must make %s red; got green\n%s", m.name, m.failing, output)
			}
			if !strings.Contains(string(output), "--- FAIL: "+m.failing+" ") {
				t.Fatalf("mutation %s did not fail %s\n%s", m.name, m.failing, output)
			}
		})
	}
}
