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

func TestCDPExistingSessionProtectedNavigation(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/existing_session_test.go"): filepath.Join(root, "test/integration/testdata/existingsession/regression.go"),
	}
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(scratch, "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", overlay, "-run", "^TestExistingSession", "./internal/caps/cdp")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("existing-session protected navigation cells: %v\n%s", err, output)
	}
}

type esIdentityMutation struct {
	name    string
	run     string
	replace [][2]string
	files   []string
}

func esIdentityMutations() []esIdentityMutation {
	taintedArm := "\tcase tainted:\n\t\tsend = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)\n\t\tg.refuseDocIdentityHoldPageLocked(h)\n"
	protectedArm := "\tcase g.docIdentityProtectedLocked(current):\n\t\tsend = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)\n\t\tg.refuseDocIdentityHoldPageLocked(h)\n\tcase !g.docIdentityOrdinaryLocked(current):\n\t\tsend = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)\n\t\tg.refuseDocIdentityHoldPageLocked(h)\n"
	chromeOrder := "\tcase chromeErrorDocument(current.url):\n\t\teventSeq, saw := g.docChromeErrorSeq[h.target]\n\t\tif saw && h.seq < eventSeq {\n\t\t\tsend = append(send, g.deliverDocIdentityDataLocked(h)...)\n\t\t} else {\n\t\t\tg.dropDeferredExtensionPaintLocked(kPaint)\n\t\t\tg.pendingPage = append(g.pendingPage, retriableContextDestroyed(h.id, h.session))\n\t\t}\n"
	refusedArm := "\tcase g.sessionRefusedLocked(h.session):\n\t\tg.refuseDocIdentityHoldPageLocked(h)\n"
	watchBad := "func (g *guard) docIdentityWatchBadLocked(url string) bool {\n\tif url == \"\" || chromeErrorDocument(url) {\n\t\treturn false\n\t}\n\tif protect.Unparseable(url) || g.o.Loaded(url) {\n\t\treturn true\n\t}\n\treturn false\n}\n"
	fromNav := "func (g *guard) noteOrdinaryDocIdentityFromNavLocked(nt *navTarget) {\n\tif nt == nil {\n\t\treturn\n\t}\n\tg.noteDocIdentityWatchURLLocked(nt.target, nt.documentURL)\n\tg.noteDocIdentityPlaceLocked(nt.target, nt.documentURL)\n\tg.recordDocIdentityChromeErrorNavLocked(nt)\n}\n"
	fromTarget := "func (g *guard) noteOrdinaryDocIdentityFromTargetLocked(t *target) {\n\tif t == nil || (t.typ != \"page\" && t.typ != \"tab\") {\n\t\treturn\n\t}\n\tg.noteDocIdentityWatchURLLocked(t.id, t.url)\n\tif g.docIdentityTargetURLIsLiveChromeErrorDestLocked(t.id, t.url) {\n\t\treturn\n\t}\n\tg.noteDocIdentityPlaceLocked(t.id, t.url)\n}\n"
	fromTargetBumpSame := "func (g *guard) noteOrdinaryDocIdentityFromTargetLocked(t *target) {\n\tif t == nil || (t.typ != \"page\" && t.typ != \"tab\") {\n\t\treturn\n\t}\n\tg.noteDocIdentityWatchURLLocked(t.id, t.url)\n\tg.noteDocIdentityPlaceLocked(t.id, t.url)\n}\n"
	samePlaceRule := "func (g *guard) docIdentityTargetURLIsLiveChromeErrorDestLocked(target, url string) bool {\n\tnt := g.navTargets[target]\n\tif nt == nil || url == \"\" || !chromeErrorDocument(nt.documentURL) || nt.errorURL == \"\" {\n\t\treturn false\n\t}\n\tif url != nt.errorURL {\n\t\treturn false\n\t}\n\tplace, saw := g.docChromeErrorPlace[target]\n\tif !saw || place != g.docPlaceSeq[target] {\n\t\treturn false\n\t}\n\tif g.docChromeErrorFailed[target] != nt.errorURL {\n\t\treturn false\n\t}\n\treturn true\n}\n"
	samePlaceNoLatest := "func (g *guard) docIdentityTargetURLIsLiveChromeErrorDestLocked(target, url string) bool {\n\tnt := g.navTargets[target]\n\tif nt == nil || url == \"\" || !chromeErrorDocument(nt.documentURL) || nt.errorURL == \"\" {\n\t\treturn false\n\t}\n\treturn url == nt.errorURL\n}\n"
	samePlaceNoChromeDoc := "func (g *guard) docIdentityTargetURLIsLiveChromeErrorDestLocked(target, url string) bool {\n\tnt := g.navTargets[target]\n\tif nt == nil || url == \"\" || nt.errorURL == \"\" {\n\t\treturn false\n\t}\n\tif url != nt.errorURL {\n\t\treturn false\n\t}\n\tplace, saw := g.docChromeErrorPlace[target]\n\tif !saw || place != g.docPlaceSeq[target] {\n\t\treturn false\n\t}\n\tif g.docChromeErrorFailed[target] != nt.errorURL {\n\t\treturn false\n\t}\n\treturn true\n}\n"
	samePlaceStillAdvances := "func (g *guard) noteOrdinaryDocIdentityFromTargetLocked(t *target) {\n\tif t == nil || (t.typ != \"page\" && t.typ != \"tab\") {\n\t\treturn\n\t}\n\tg.noteDocIdentityWatchURLLocked(t.id, t.url)\n\t_ = g.docIdentityTargetURLIsLiveChromeErrorDestLocked(t.id, t.url)\n\tg.noteDocIdentityPlaceLocked(t.id, t.url)\n}\n"
	samePlaceAlwaysWhenChrome := "func (g *guard) docIdentityTargetURLIsLiveChromeErrorDestLocked(target, url string) bool {\n\tnt := g.navTargets[target]\n\tif nt == nil || url == \"\" || !chromeErrorDocument(nt.documentURL) {\n\t\treturn false\n\t}\n\tplace, saw := g.docChromeErrorPlace[target]\n\tif !saw || place != g.docPlaceSeq[target] {\n\t\treturn false\n\t}\n\tif g.docChromeErrorFailed[target] != nt.errorURL {\n\t\treturn false\n\t}\n\treturn true\n}\n"
	samePlaceNoFailed := "func (g *guard) docIdentityTargetURLIsLiveChromeErrorDestLocked(target, url string) bool {\n\tnt := g.navTargets[target]\n\tif nt == nil || url == \"\" || !chromeErrorDocument(nt.documentURL) || nt.errorURL == \"\" {\n\t\treturn false\n\t}\n\tif url != nt.errorURL {\n\t\treturn false\n\t}\n\tplace, saw := g.docChromeErrorPlace[target]\n\tif !saw || place != g.docPlaceSeq[target] {\n\t\treturn false\n\t}\n\treturn true\n}\n"
	liveMatch := "func (g *guard) docIdentityLiveChromeErrorMatchLocked(target, infoURL string) bool {\n\tif !g.docIdentityLiveChromeErrorNavLocked(target) {\n\t\treturn false\n\t}\n\tnt := g.navTargets[target]\n\treturn infoURL != \"\" && infoURL == nt.errorURL\n}\n"
	liveMatchNoEq := "func (g *guard) docIdentityLiveChromeErrorMatchLocked(target, infoURL string) bool {\n\t_ = infoURL\n\treturn g.docIdentityLiveChromeErrorNavLocked(target)\n}\n"
	liveNav := "func (g *guard) docIdentityLiveChromeErrorNavLocked(target string) bool {\n\tnt := g.navTargets[target]\n\tif nt == nil || !chromeErrorDocument(nt.documentURL) || nt.errorURL == \"\" {\n\t\treturn false\n\t}\n\tplace, saw := g.docChromeErrorPlace[target]\n\tif !saw || place != g.docPlaceSeq[target] {\n\t\treturn false\n\t}\n\tif g.docChromeErrorFailed[target] != nt.errorURL {\n\t\treturn false\n\t}\n\treturn true\n}\n"
	liveNavNoLatest := "func (g *guard) docIdentityLiveChromeErrorNavLocked(target string) bool {\n\tnt := g.navTargets[target]\n\tif nt == nil || !chromeErrorDocument(nt.documentURL) || nt.errorURL == \"\" {\n\t\treturn false\n\t}\n\tif g.docChromeErrorFailed[target] != nt.errorURL {\n\t\treturn false\n\t}\n\treturn true\n}\n"
	matchArm := "\tcase g.docIdentityLiveChromeErrorMatchLocked(h.target, current.url) && g.docIdentityProtectedLocked(current):\n\t\tg.dropDeferredExtensionPaintLocked(kPaint)\n\t\tg.pendingPage = append(g.pendingPage, retriableContextDestroyed(h.id, h.session))\n"
	matchArmNoProtected := "\tcase g.docIdentityLiveChromeErrorMatchLocked(h.target, current.url):\n\t\tg.dropDeferredExtensionPaintLocked(kPaint)\n\t\tg.pendingPage = append(g.pendingPage, retriableContextDestroyed(h.id, h.session))\n"
	watchURL := "func (g *guard) noteDocIdentityWatchURLLocked(target, url string) {\n\tif target == \"\" || !g.docIdentityWatchBadLocked(url) {\n\t\treturn\n\t}\n\tg.bumpDocIdentityTaintLocked(target)\n}\n"
	startedAllow := "func (g *guard) docIdentityStartedNetworkAllowLocked(target, url string) bool {\n\tif target == \"\" || url == \"\" || g.o == nil || chromeErrorDocument(url) {\n\t\treturn false\n\t}\n\tif !g.docIdentityNavFetchArmedLocked(target) {\n\t\treturn false\n\t}\n\ts := protect.Scheme(url)\n\tif s != \"http\" && s != \"https\" {\n\t\treturn false\n\t}\n\tif protect.Unparseable(url) {\n\t\treturn false\n\t}\n\tif !g.o.URL(url) {\n\t\treturn false\n\t}\n\treturn g.docIdentityFetchGateCoversLocked(url)\n}\n"
	navArmed := "func (g *guard) docIdentityNavFetchArmedLocked(target string) bool {\n\tnt := g.navTargets[target]\n\treturn nt != nil && nt.session != \"\" && !nt.failed && nt.fetchArmed\n}\n"
	startedNav := "func (g *guard) noteDocIdentityStartedNavigatingLocked(target, url string) {\n\tif target == \"\" {\n\t\treturn\n\t}\n\tif g.docIdentityStartedNetworkAllowLocked(target, url) {\n\t\treturn\n\t}\n\tg.noteDocIdentityWatchURLLocked(target, url)\n}\n"
	ordinaryEmpty := "func (g *guard) docIdentityOrdinaryLocked(id docIdentity) bool {\n\tif id.url == \"\" || g.docIdentityProtectedLocked(id) || chromeErrorDocument(id.url) {\n\t\treturn false\n\t}\n\tif protect.Inheriting(id.url) {\n\t\treturn true\n\t}\n\tif strings.HasPrefix(strings.ToLower(id.url), \"http://\") || strings.HasPrefix(strings.ToLower(id.url), \"https://\") {\n\t\treturn !g.o.Loaded(id.url)\n\t}\n\treturn !g.o.Loaded(id.url)\n}\n"
	ordinaryEmptyAsOrdinary := "func (g *guard) docIdentityOrdinaryLocked(id docIdentity) bool {\n\tif g.docIdentityProtectedLocked(id) || chromeErrorDocument(id.url) {\n\t\treturn false\n\t}\n\tif id.url == \"\" || protect.Inheriting(id.url) {\n\t\treturn true\n\t}\n\tif strings.HasPrefix(strings.ToLower(id.url), \"http://\") || strings.HasPrefix(strings.ToLower(id.url), \"https://\") {\n\t\treturn !g.o.Loaded(id.url)\n\t}\n\treturn !g.o.Loaded(id.url)\n}\n"
	bump := "func (g *guard) bumpDocIdentityTaintLocked(target string) {\n\tif target == \"\" {\n\t\treturn\n\t}\n\tif g.docIdentityTaint == nil {\n\t\tg.docIdentityTaint = map[string]uint64{}\n\t}\n\tg.docIdentityTaint[target]++\n}\n"
	pinnedNeeds := "func (g *guard) docIdentityPinnedNeedsHoldLocked(c *command) bool {\n\tif c == nil || !docIdentityPinned(c) {\n\t\treturn false\n\t}\n\tt := g.sessionTargetLocked(c.session)\n\tif t == nil || !navTypes[t.typ] {\n\t\treturn false\n\t}\n\tplace := g.docPlaceSeq[t.id]\n\tctxPlace, ok := g.shownContextPlaceSeqLocked(t.id, c.params)\n\tif !ok {\n\t\treturn false\n\t}\n\tif ctxPlace < place {\n\t\treturn true\n\t}\n\tcur := g.currentDocIdentityLocked(t.id)\n\tif cur.url == \"\" {\n\t\tcur.url = t.url\n\t}\n\treturn g.docIdentityWatchBadLocked(cur.url) || g.docIdentityWatchBadLocked(t.url)\n}\n"
	identityThenExt := "func (g *guard) answerIdentityOrExtensionLocked(k callKey, m *message, method, target string, v browserVerdict) browserVerdict {\n\tif v.forward == nil {\n\t\tg.dropDocIdentityCallLocked(k)\n\t\treturn v\n\t}\n\tif send, page, held := g.holdDocIdentityReplyLocked(k, m, v.forward); held {\n\t\tif method != \"\" && protect.ExtensionShownMethod(method) && g.extensionFramesPresentLocked(target) {\n\t\t\tif h := g.docIdentityHolds[k]; h != nil {\n\t\t\t\th.method = method\n\t\t\t\th.extensionAfter = true\n\t\t\t\th.extensionFrames = g.extensionFrameIDsLocked(target)\n\t\t\t\tarmed := g.armExtensionPaintLocked(m, v.forward, target, h.extensionFrames, true)\n\t\t\t\tv.extensionPaints = append(v.extensionPaints, armed.extensionPaints...)\n\t\t\t\tif armed.forward != nil {\n\t\t\t\t\tg.dropDeferredExtensionPaintLocked(k)\n\t\t\t\t\tg.dropDocIdentityHoldLocked(k)\n\t\t\t\t\tv.forward = nil\n\t\t\t\t\tv.send = append(v.send, send...)\n\t\t\t\t\tv.page = append(v.page, page...)\n\t\t\t\t\tv.page = append(v.page, armed.forward)\n\t\t\t\t\treturn v\n\t\t\t\t}\n\t\t\t}\n\t\t}\n\t\tv.forward = nil\n\t\tv.send = append(v.send, send...)\n\t\tv.page = append(v.page, page...)\n\t\treturn v\n\t}\n\tif method != \"\" && protect.ExtensionShownMethod(method) && g.extensionFramesPresentLocked(target) {\n\t\tg.dropDocIdentityCallLocked(k)\n\t\treturn g.holdExtensionPaintLocked(m, v.forward, target, g.extensionFrameIDsLocked(target))\n\t}\n\tg.dropDocIdentityCallLocked(k)\n\treturn v\n}\n"
	restoreExtDrop := "func (g *guard) answerIdentityOrExtensionLocked(k callKey, m *message, method, target string, v browserVerdict) browserVerdict {\n\tif v.forward == nil {\n\t\tg.dropDocIdentityCallLocked(k)\n\t\treturn v\n\t}\n\tif method != \"\" && protect.ExtensionShownMethod(method) && g.extensionFramesPresentLocked(target) {\n\t\tg.dropDocIdentityCallLocked(k)\n\t\treturn g.holdExtensionPaintLocked(m, v.forward, target, g.extensionFrameIDsLocked(target))\n\t}\n\tif send, page, held := g.holdDocIdentityReplyLocked(k, m, v.forward); held {\n\t\tv.forward = nil\n\t\tv.send = append(v.send, send...)\n\t\tv.page = append(v.page, page...)\n\t\treturn v\n\t}\n\tg.dropDocIdentityCallLocked(k)\n\treturn v\n}\n"
	emptyRelayClosed := "\tif g.relaySession == \"\" {\n\t\treturn nil, [][]byte{retriableContextDestroyed(*m.ID, m.SessionID)}, true\n\t}\n"
	fetchGateCanon := "func (g *guard) docIdentityFetchGateCoversLocked(url string) bool {\n\tif g.o == nil || url == \"\" {\n\t\treturn false\n\t}\n\tcan, ok := protect.Canonical(url)\n\tif !ok || can == \"\" {\n\t\treturn false\n\t}\n\tfor _, p := range g.o.DocumentPatterns() {\n\t\tif fetchURLPatternMatch(p, can) {\n\t\t\treturn true\n\t\t}\n\t}\n\treturn false\n}\n"
	fetchGateOR := "func (g *guard) docIdentityFetchGateCoversLocked(url string) bool {\n\tif g.o == nil || url == \"\" {\n\t\treturn false\n\t}\n\tcands := []string{url}\n\tif lower := strings.ToLower(url); lower != url {\n\t\tcands = append(cands, lower)\n\t}\n\tif can, ok := protect.Canonical(url); ok && can != \"\" {\n\t\tcands = append(cands, can)\n\t\tif lower := strings.ToLower(can); lower != can {\n\t\t\tcands = append(cands, lower)\n\t\t}\n\t}\n\tfor _, p := range g.o.DocumentPatterns() {\n\t\tfor _, cand := range cands {\n\t\t\tif fetchURLPatternMatch(p, cand) {\n\t\t\t\treturn true\n\t\t\t}\n\t\t}\n\t}\n\treturn false\n}\n"
	return []esIdentityMutation{
		{
			name: "drop-taint-check",
			run:  "^TestExistingSessionWindowTaint(ProtectedEventCutsDespiteOrdinaryPostRead|UnparseableURLCutsDespiteOrdinaryPostRead|EmptyToWrappedUnresolvableCutsDespiteOrdinaryPostRead)$",
			replace: [][2]string{{
				taintedArm,
				"\tcase tainted:\n\t\tg.pendingPage = append(g.pendingPage, h.data)\n",
			}},
		},
		{
			name: "skip-post-read-classification",
			run:  "^TestExistingSessionOutcomeAProtectedCuts$",
			replace: [][2]string{{
				protectedArm,
				"\tcase g.docIdentityProtectedLocked(current):\n\t\tg.pendingPage = append(g.pendingPage, h.data)\n\tcase !g.docIdentityOrdinaryLocked(current):\n\t\tg.pendingPage = append(g.pendingPage, h.data)\n",
			}},
		},
		{
			name: "empty-relay-fail-open",
			run:  "^TestExistingSessionEmptyRelaySessionFailClosedRetriable$",
			replace: [][2]string{{
				emptyRelayClosed,
				"\tif g.relaySession == \"\" {\n\t\treturn nil, nil, false\n\t}\n",
			}},
		},
		{
			name: "fetch-gate-or-candidates",
			run:  "^TestExistingSessionFetchGateCanonicalOnly/raw-matches-canonical-fails$",
			replace: [][2]string{{
				fetchGateCanon,
				fetchGateOR,
			}},
		},
		{
			name: "allow-without-fetch-armed",
			run:  "^TestExistingSessionE7AllowRequiresFetchArmed/(fetch-enable-failed|mid-re-enable|detached-nav-session)$",
			replace: [][2]string{{
				navArmed,
				"func (g *guard) docIdentityNavFetchArmedLocked(target string) bool {\n\t_ = target\n\treturn true\n}\n",
			}},
		},
		{
			name: "empty-url-as-ordinary",
			run:  "^TestExistingSessionEmptyPostReadURL/empty-at-bound-retriable$",
			replace: [][2]string{
				{
					"\tif result.TargetInfo.URL == \"\" {\n\t\task.emptyTries++\n\t\tif ask.emptyTries < maxDocIdentityEmptyAsks && g.docIdentityHoldsWithinDeadlineLocked(target) {\n\t\t\task.infoDone = false\n\t\t\task.infoID = docIdentity{}\n\t\t\task.askingInfo = true\n\t\t\treturn [][]byte{g.callbackLocked(g.relaySession, \"Target.getTargetInfo\", map[string]any{\"targetId\": target}, func(m message) [][]byte {\n\t\t\t\treturn g.answeredDocIdentityInfoLocked(target, m)\n\t\t\t})}\n\t\t}\n\t\tdelete(g.docIdentityAsks, target)\n\t\treturn g.failDocIdentityHoldsLocked(target, false)\n\t}\n",
					"",
				},
				{
					ordinaryEmpty,
					ordinaryEmptyAsOrdinary,
				},
			},
		},
		{
			name:  "duplicate-paint-arm-keeps-identity",
			run:   "^TestExistingSessionDuplicatePaintArmDropsIdentityHold$",
			files: []string{"paint.go"},
			replace: [][2]string{{
				"\t\t\t\t\tg.dropDeferredExtensionPaintLocked(k)\n\t\t\t\t\tg.dropDocIdentityHoldLocked(k)\n",
				"\t\t\t\t\tg.dropDeferredExtensionPaintLocked(k)\n",
			}},
		},
		{
			name: "delete-chrome-error-ordering",
			run:  "^TestExistingSessionChromeErrorNeverLearnSecretRefused$",
			replace: [][2]string{{
				chromeOrder,
				"\tcase chromeErrorDocument(current.url):\n\t\tg.pendingPage = append(g.pendingPage, h.data)\n",
			}},
		},
		{
			name: "chrome-error-taints-again",
			run:  "^TestExistingSessionOutlivedProtectedErrorPageCommitKeepsTrigger$",
			replace: [][2]string{
				{
					watchBad,
					"func (g *guard) docIdentityWatchBadLocked(url string) bool {\n\tif url == \"\" {\n\t\treturn false\n\t}\n\tif chromeErrorDocument(url) || protect.Unparseable(url) || g.o.Loaded(url) {\n\t\treturn true\n\t}\n\treturn false\n}\n",
				},
				{
					chromeOrder,
					"\tcase chromeErrorDocument(current.url):\n\t\tsend = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)\n\t\tg.pendingPage = append(g.pendingPage, refusal(h.id, h.session, refusedTarget))\n",
				},
			},
		},
		{
			name: "no-taint-unparseable-or-empty-to-wrapped",
			run:  "^TestExistingSessionWindowTaint(UnparseableURLCutsDespiteOrdinaryPostRead|EmptyToWrappedUnresolvableCutsDespiteOrdinaryPostRead)$",
			replace: [][2]string{{
				watchBad,
				"func (g *guard) docIdentityWatchBadLocked(url string) bool {\n\tif url == \"\" || chromeErrorDocument(url) {\n\t\treturn false\n\t}\n\tif g.o.URL(url) {\n\t\treturn true\n\t}\n\treturn false\n}\n",
			}},
		},
		{
			name: "drop-nav-ordinary-note",
			run:  "^TestExistingSessionP1TaintSourceInFlightReply/(frameNavigated-protected|frameNavigated-unparseable|frameNavigated-empty-to-wrapped|frameNavigated-bfcache-restore-protected|frameNavigated-prerender-type-protected|history-navigateToHistoryEntry-then-frameNavigated)$",
			replace: [][2]string{{
				fromNav,
				"func (g *guard) noteOrdinaryDocIdentityFromNavLocked(nt *navTarget) {\n\t_ = nt\n}\n",
			}},
		},
		{
			name: "drop-newly-protected-taint",
			run:  "^TestExistingSessionP1TaintSourceInFlightReply/(targetInfoChanged-newly-protected|targetInfoChanged-unparseable|targetCreated-protected-observe)$",
			replace: [][2]string{{
				fromTarget,
				"func (g *guard) noteOrdinaryDocIdentityFromTargetLocked(t *target) {\n\t_ = t\n}\n",
			}},
		},
		{
			name:  "drop-frame-started-navigating-bump",
			run:   "^TestExistingSessionP1TaintSourceInFlightReply/frameStartedNavigating-protected$",
			files: []string{"navguard.go"},
			replace: [][2]string{{
				"\tcase \"Page.frameStartedNavigating\":\n\t\tvar started struct {\n\t\t\tFrameID        string `json:\"frameId\"`\n\t\t\tURL            string `json:\"url\"`\n\t\t\tNavigationType string `json:\"navigationType\"`\n\t\t}\n\t\tif json.Unmarshal(m.Params, &started) == nil && started.FrameID == nt.target && !sameDocument[started.NavigationType] {\n\t\t\tg.noteDocIdentityStartedNavigatingLocked(nt.target, started.URL)\n\t\t}\n",
				"\tcase \"Page.frameStartedNavigating\":\n",
			}},
		},
		{
			name: "e7-bump-on-network-form",
			run:  "^TestExistingSessionE7EvaluateWithNavigationNetworkDelivers$",
			replace: [][2]string{{
				startedAllow,
				"func (g *guard) docIdentityStartedNetworkAllowLocked(target, url string) bool {\n\t_, _ = target, url\n\treturn false\n}\n",
			}},
		},
		{
			name: "e7-no-bump-on-wrapper",
			run:  "^TestExistingSessionE7EvaluateWithNavigationWrapperCuts$",
			replace: [][2]string{{
				startedNav,
				"func (g *guard) noteDocIdentityStartedNavigatingLocked(target, url string) {\n\t_, _ = target, url\n}\n",
			}},
		},
		{
			name: "e7-allow-list-widened-unknown-scheme",
			run:  "^TestExistingSessionE7EvaluateWithNavigationWrapperCuts$",
			replace: [][2]string{{
				startedAllow,
				"func (g *guard) docIdentityStartedNetworkAllowLocked(target, url string) bool {\n\tif target == \"\" || url == \"\" || g.o == nil || chromeErrorDocument(url) {\n\t\treturn false\n\t}\n\treturn g.o.URL(url)\n}\n",
			}},
		},
		{
			name: "e7-allow-latched-on-first-network-started",
			run:  "^TestExistingSessionE7EvaluateWithNavigationNetworkThenWrapperCuts$",
			replace: [][2]string{{
				startedNav,
				"func (g *guard) noteDocIdentityStartedNavigatingLocked(target, url string) {\n\tif target == \"\" {\n\t\treturn\n\t}\n\tif g.docIdentityStartedNetworkAllowLocked(target, url) {\n\t\tif g.docPlaceURL == nil {\n\t\t\tg.docPlaceURL = map[string]string{}\n\t\t}\n\t\tg.docPlaceURL[target+\"\\x00started-allow\"] = \"1\"\n\t\treturn\n\t}\n\tif g.docPlaceURL[target+\"\\x00started-allow\"] == \"1\" {\n\t\treturn\n\t}\n\tg.noteDocIdentityWatchURLLocked(target, url)\n}\n",
			}},
		},
		{
			name:  "881-deferred-paint-calls-locking-expire",
			run:   "^TestExistingSessionExpiredDeferredPaintProbeUnderLockReturns$",
			files: []string{"extensionframe.go"},
			replace: [][2]string{{
				"\tif time.Now().After(h.deadline) {\n\t\tpage, releases := g.expireExtensionPaintLocked(k, h.generation)\n",
				"\tif time.Now().After(h.deadline) {\n\t\tpage, releases := g.expireExtensionPaint(k, h.generation)\n",
			}},
		},
		{
			name: "drop-refused-session-check",
			run:  "^TestExistingSessionP1RefusedSessionMustNotDeliverHeldReply$",
			replace: [][2]string{{
				refusedArm,
				"",
			}},
		},
		{
			name: "drop-watch-url-note",
			run:  "^TestExistingSessionP1TaintSourceInFlightReply/(frameNavigated-protected|watch-session-url-already-protected-observe|frameStartedNavigating-protected)$",
			replace: [][2]string{{
				watchURL,
				"func (g *guard) noteDocIdentityWatchURLLocked(target, url string) {\n\t_, _ = target, url\n}\n",
			}},
		},
		{
			name: "drop-bump-taint",
			run:  "^TestExistingSessionP1TaintSourceInFlightReply/(frameNavigated-protected|targetInfoChanged-newly-protected)$",
			replace: [][2]string{{
				bump,
				"func (g *guard) bumpDocIdentityTaintLocked(target string) {\n\t_ = target\n}\n",
			}},
		},
		{
			name:  "restore-extension-drop-identity",
			run:   "^TestExistingSessionP1ExtensionShownPaintIdentityThenShown/identity-refuse-leaves-no-extension-hold$",
			files: []string{"paint.go"},
			replace: [][2]string{{
				identityThenExt,
				restoreExtDrop,
			}},
		},
		{
			name: "pinned-evaluate-window-off",
			run:  "^TestExistingSessionP1PinnedEvaluateOnShownViewSourceContext/blob-opaque-before-place$",
			replace: [][2]string{{
				pinnedNeeds,
				"func (g *guard) docIdentityPinnedNeedsHoldLocked(c *command) bool {\n\t_ = c\n\treturn false\n}\n",
			}},
		},
		{
			name: "chrome-error-no-cut-drop-equality",
			run:  "^TestExistingSessionChromeErrorProtectedInfoDifferentUnreachableCuts$",
			replace: [][2]string{{
				liveMatch,
				liveMatchNoEq,
			}},
		},
		{
			name: "chrome-error-no-cut-drop-latest",
			run:  "^TestExistingSessionChromeErrorThenLaterProtectedCommitCuts/stale-place$",
			replace: [][2]string{{
				liveNav,
				liveNavNoLatest,
			}},
		},
		{
			name: "chrome-error-no-cut-drop-preference",
			run:  "^TestExistingSessionChromeErrorTargetInfoLagsProtectedUnreachableNoCut$",
			replace: [][2]string{{
				matchArm,
				"",
			}},
		},
		{
			name: "chrome-error-match-drop-protected",
			run:  "^TestExistingSessionChromeErrorOrdinaryErrorControlShapes$",
			replace: [][2]string{{
				matchArm,
				matchArmNoProtected,
			}},
		},
		{
			name: "chrome-error-tainted-before-match",
			run:  "^TestExistingSessionChromeErrorSameURLTargetInfoTaintMatchBeforeTainted/match-despite-taint-recovery$",
			replace: [][2]string{{
				matchArm + protectedArm + taintedArm,
				taintedArm + matchArm + protectedArm,
			}},
		},
		{
			name: "chrome-error-same-place-bump-on-same-url",
			run:  "^TestExistingSessionChromeErrorSamePlaceTargetInfoOrders/nav-then-target-same$",
			replace: [][2]string{{
				fromTarget,
				fromTargetBumpSame,
			}},
		},
		{
			name: "chrome-error-same-place-different-url-keeps",
			run:  "^TestExistingSessionChromeErrorSamePlaceTargetInfoOrders/nav-then-target-different$",
			replace: [][2]string{{
				samePlaceRule,
				samePlaceAlwaysWhenChrome,
			}},
		},
		{
			name: "chrome-error-same-place-drop-latest",
			run:  "^TestExistingSessionChromeErrorSamePlaceTargetInfoOrders/nav-then-same-then-lagging-ordinary$",
			replace: [][2]string{{
				samePlaceRule,
				samePlaceNoLatest,
			}},
		},
		{
			name: "chrome-error-same-place-without-chrome-doc",
			run:  "^TestExistingSessionChromeErrorSamePlaceTargetInfoOrders/same-place-requires-chrome-error-doc$",
			replace: [][2]string{{
				samePlaceRule,
				samePlaceNoChromeDoc,
			}},
		},
		{
			name: "chrome-error-dest-drop-failed-align",
			run:  "^TestExistingSessionChromeErrorDestFailedAlignSnapshotRewrite$",
			replace: [][2]string{{
				samePlaceRule,
				samePlaceNoFailed,
			}},
		},
		{
			name: "chrome-error-same-place-still-advances",
			run:  "^TestExistingSessionChromeErrorSamePlaceTargetInfoOrders/nav-then-target-same$",
			replace: [][2]string{{
				fromTarget,
				samePlaceStillAdvances,
			}},
		},
	}
}

func esMutateFiles(t *testing.T, root string, scratch string, m esIdentityMutation) map[string]string {
	t.Helper()
	out := map[string]string{}
	files := m.files
	if len(files) == 0 {
		files = []string{"documentidentity.go"}
	}
	for _, name := range files {
		srcPath := filepath.Join(root, "internal/caps/cdp", name)
		src, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		for _, pair := range m.replace {
			if pair[0] == "" {
				t.Fatalf("mutation %s: empty anchor", m.name)
			}
			if !strings.Contains(text, pair[0]) {
				t.Fatalf("mutation %s: anchor missing from %s:\n%s", m.name, name, pair[0])
			}
			text = strings.Replace(text, pair[0], pair[1], 1)
		}
		path := filepath.Join(scratch, "mutation_"+m.name+"_"+name)
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		out[srcPath] = path
	}
	return out
}

func TestCDPExistingSessionIdentityMutations(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	regression := filepath.Join(root, "test/integration/testdata/existingsession/regression.go")
	for _, m := range esIdentityMutations() {
		m := m
		t.Run(m.name, func(t *testing.T) {
			scratch := t.TempDir()
			mutants := esMutateFiles(t, root, scratch, m)
			replacements := map[string]string{
				filepath.Join(root, "internal/caps/cdp/existing_session_test.go"): regression,
			}
			for src, mutant := range mutants {
				replacements[src] = mutant
			}
			data, err := json.Marshal(map[string]any{"Replace": replacements})
			if err != nil {
				t.Fatal(err)
			}
			overlay := filepath.Join(scratch, "overlay.json")
			if err := os.WriteFile(overlay, data, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", m.run, "./internal/caps/cdp")
			command.Dir = root
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("mutation %s must make contract rows red (-run %s); got green\n%s", m.name, m.run, output)
			}
		})
	}
}
