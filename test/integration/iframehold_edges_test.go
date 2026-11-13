package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCDPIframeHoldAncestorEdges(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/iframe_hold_edges_test.go"): filepath.Join(root, "test/integration/testdata/iframehold/regression.go"),
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
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", "^Test(IframeHold(OrdinaryURLUnderSeven|SrcdocUnderSeven|NestedUnderSevenOrdinary|OrdinaryURLUnderVault|SrcdocUnderVault|NestedUnderVaultSrcdoc|UnknownParentTargetStaysHeld|InProcessFrameUnderOrdinaryParent|RecordedFrameUnderSeven|StickyAfterAncestorNavigatesAway|OrdinaryUnderOrdinary|URLLessStaysPendingUntilURL|BrowserLevelNilParentStaysUnresolved|BrowserLevelChromeErrorReleasesForConfirm|ChromeErrorFrameTreeProtectedParent|ChromeErrorFrameTreeHeldOrUnknownParent|ChromeErrorFrameTreeOrdinaryOrMissingParent|UnknownAncestryPropagatesToChild|DeepUnknownAncestryStaysHidden|ParentFrameCycleStaysHidden|OrdinaryDeepWithinLimitShown|OrdinaryDeepBeyondLimitRefusesAncestry|ProtectedAncestorHidesURLLess|MissingParentIdFrameTreeUnderSeven|BrowserAttachFollowsOwnerProtect|BrowserAttachOwnerHeldRefused|AttachSessionDisagreesProtected|OrdinaryNestedAttachReachesOwner|AttachChainMissesOwnerDisputed|FrameParentsPrunedOnDestroy|FrameParentsCapFailsClosed|LateParentUnderSevenRedecidesDescendants|LateParentUnderVaultRedecidesDescendants|OrdinaryLateParentStaysShown|LateParentCascadesWorkerUnderSeven|LateParentCascadesWorkerUnderVault|OrdinaryLateParentWorkerStaysDelivered|LateParentCascadesWorkerOfWorkerUnderSeven|LateParentCascadesWorkerOfWorkerUnderVault|OrdinaryLateParentWorkerOfWorkerStaysDelivered|FrameAttachedOnTrackedPageRedecides|ExtensionFrameUnderOrdinaryKeepsMainRule|ExtensionFrameUnderSevenProtected|TabOwnerEmptyParentThenPage|PageThenTabOwnerEmptyParent|TabOwnerNestedChainReachesPage|BrowserOwnerEmptyParentThenPage|BrowserOwnerNestedChainNoDispute|UnknownOwnerTypeStillDisputes|UnresolvedTabOwnerFailsClosed|AmbiguousTabPagesFailClosed|DifferentPageOwnerStillDisputes|TabSessionFollowMatchesResolvedPage|UnconfirmedPageHoldsNotProtects|UnconfirmedPageThenOrdinaryDelivers|UnconfirmedPageThenSevenProtects|ConfirmPendingParentDoesNotStickyProtect|ConfirmSettledSevenParentProtects|ContentScriptWorldDoesNotProtectSibling|PollutedExtensionChildFrameDoesNotProtectOrdinaryPage|DefaultProtectedContextStillLocksPage)|Review(MissingAncestry|NestedHeldAncestor|AttachOverwritesAncestry|OwnerUnconfirmed|PlaceholderOverwrite))$", "./internal/caps/cdp")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("iframe hold ancestor edges: %v\n%s", err, output)
	}
}
