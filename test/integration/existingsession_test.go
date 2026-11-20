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
}

func esIdentityMutations() []esIdentityMutation {
	taintedArm := "\tcase tainted:\n\t\tsend = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)\n\t\tg.pendingPage = append(g.pendingPage, refusal(h.id, h.session, refusedTarget))\n"
	protectedArm := "\tcase g.docIdentityProtectedLocked(current):\n\t\tsend = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)\n\t\tg.pendingPage = append(g.pendingPage, refusal(h.id, h.session, refusedTarget))\n\tcase !g.docIdentityOrdinaryLocked(current):\n\t\tsend = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)\n\t\tg.pendingPage = append(g.pendingPage, refusal(h.id, h.session, refusedTarget))\n"
	releaseRefresh := "\tdefault:\n\t\tif current.full {\n\t\t\tg.noteOrdinaryDocIdentityLocked(h.target, current.frameID, current.loaderID, current.url, true)\n\t\t}\n\t\tg.pendingPage = append(g.pendingPage, h.data)\n"
	infoRefresh := "\tif result.TargetInfo.URL != \"\" && !g.o.Loaded(result.TargetInfo.URL) && !chromeErrorDocument(result.TargetInfo.URL) {\n\t\tg.noteOrdinaryDocIdentityLocked(target, target, \"\", result.TargetInfo.URL, true)\n\t}\n"
	chromeOrder := "\tcase chromeErrorDocument(current.url):\n\t\teventSeq, saw := g.docChromeErrorSeq[h.target]\n\t\tif saw && h.seq < eventSeq {\n\t\t\tg.pendingPage = append(g.pendingPage, h.data)\n\t\t} else {\n\t\t\tg.pendingPage = append(g.pendingPage, retriableContextDestroyed(h.id, h.session))\n\t\t}\n"
	watchBad := "func (g *guard) docIdentityWatchBadLocked(url string) bool {\n\tif url == \"\" || chromeErrorDocument(url) {\n\t\treturn false\n\t}\n\tif protect.Unparseable(url) || g.o.Loaded(url) {\n\t\treturn true\n\t}\n\treturn false\n}\n"
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
			name: "no-baseline-refresh-on-accept",
			run:  "^TestExistingSession(OutcomeBOrdinaryIdentityChangeAcceptsAndAdvancesBaseline|BaselineUpdatesOnOrdinaryNavigation)$",
			replace: [][2]string{
				{releaseRefresh, "\tdefault:\n\t\tg.pendingPage = append(g.pendingPage, h.data)\n"},
				{infoRefresh, ""},
			},
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
			run:  "^TestExistingSessionP1TaintSourceInFlightReply/(frameNavigated-protected|frameNavigated-unparseable|frameNavigated-empty-to-wrapped|frameNavigated-bfcache-restore-protected|history-navigateToHistoryEntry-then-frameNavigated)$",
			replace: [][2]string{{
				"func (g *guard) noteOrdinaryDocIdentityFromNavLocked(nt *navTarget) {\n\tif nt == nil {\n\t\treturn\n\t}\n\tg.noteDocIdentityChromeErrorCommitLocked(nt.target, nt.documentURL)\n\tg.noteDocIdentityWatchURLLocked(nt.target, nt.documentURL)\n}\n",
				"func (g *guard) noteOrdinaryDocIdentityFromNavLocked(nt *navTarget) {\n\t_ = nt\n}\n",
			}},
		},
		{
			name: "drop-target-ordinary-note",
			run:  "^TestExistingSessionP1TaintSourceInFlightReply/watch-session-url-already-protected-observe$",
			replace: [][2]string{{
				"func (g *guard) noteOrdinaryDocIdentityFromTargetLocked(t *target) {\n\tif t == nil || (t.typ != \"page\" && t.typ != \"tab\") {\n\t\treturn\n\t}\n\tg.noteDocIdentityChromeErrorCommitLocked(t.id, t.url)\n\tg.noteDocIdentityWatchURLLocked(t.id, t.url)\n}\n",
				"func (g *guard) noteOrdinaryDocIdentityFromTargetLocked(t *target) {\n\t_ = t\n}\n",
			}},
		},
		{
			name: "drop-watch-url-note",
			run:  "^TestExistingSessionP1TaintSourceInFlightReply/(frameNavigated-protected|watch-session-url-already-protected-observe)$",
			replace: [][2]string{{
				"func (g *guard) noteDocIdentityWatchURLLocked(target, url string) {\n\tif target == \"\" || !g.docIdentityWatchBadLocked(url) {\n\t\treturn\n\t}\n\tg.bumpDocIdentityTaintLocked(target)\n}\n",
				"func (g *guard) noteDocIdentityWatchURLLocked(target, url string) {\n\t_, _ = target, url\n}\n",
			}},
		},
		{
			name: "drop-bump-taint",
			run:  "^TestExistingSessionP1TaintSourceInFlightReply/(frameNavigated-protected|targetInfoChanged-newly-protected)$",
			replace: [][2]string{{
				"func (g *guard) bumpDocIdentityTaintLocked(target string) {\n\tif target == \"\" {\n\t\treturn\n\t}\n\tif g.docIdentityTaint == nil {\n\t\tg.docIdentityTaint = map[string]uint64{}\n\t}\n\tg.docIdentityTaint[target]++\n}\n",
				"func (g *guard) bumpDocIdentityTaintLocked(target string) {\n\t_ = target\n}\n",
			}},
		},
	}
}

func esMutateDocumentIdentity(t *testing.T, root string, scratch string, m esIdentityMutation) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, "internal/caps/cdp/documentidentity.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, pair := range m.replace {
		if pair[0] == "" {
			t.Fatalf("mutation %s: empty anchor", m.name)
		}
		if !strings.Contains(text, pair[0]) {
			t.Fatalf("mutation %s: anchor missing from production documentidentity.go:\n%s", m.name, pair[0])
		}
		text = strings.Replace(text, pair[0], pair[1], 1)
	}
	out := filepath.Join(scratch, "mutation_"+m.name+".go")
	if err := os.WriteFile(out, []byte(text), 0600); err != nil {
		t.Fatal(err)
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
			mutant := esMutateDocumentIdentity(t, root, scratch, m)
			replacements := map[string]string{
				filepath.Join(root, "internal/caps/cdp/existing_session_test.go"): regression,
				filepath.Join(root, "internal/caps/cdp/documentidentity.go"):      mutant,
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
