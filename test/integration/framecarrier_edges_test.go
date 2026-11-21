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

func frameCarrierOverlayRun(t *testing.T, mutation, run string, wantFail bool) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "test/integration/testdata/framecarrier")
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/frame_carrier_edges_test.go"): filepath.Join(fixture, "regression.go"),
	}
	if mutation != "" {
		replacements[filepath.Join(root, "internal/caps/cdp/frame_carrier_switches.go")] = filepath.Join(fixture, mutation)
	}
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", run, "./internal/caps/cdp")
	command.Dir = root
	output, err := command.CombinedOutput()
	if wantFail {
		if err == nil {
			t.Fatalf("mutant %s must fail the named row %s\n%s", mutation, run, output)
		}
		return
	}
	if err != nil {
		t.Fatalf("%s: %v\n%s", run, err, output)
	}
}

func TestCDPFrameCarrierEdges(t *testing.T) {
	frameCarrierOverlayRun(t, "", "^TestFrameCarrier", false)
}

func TestCDPFrameCarrierMutationURLOnly(t *testing.T) {
	frameCarrierOverlayRun(t, "mutation_url_only.go", "^TestFrameCarrierWrapperChildTreePrelearn$", true)
}

func TestCDPFrameCarrierMutationOriginIgnored(t *testing.T) {
	frameCarrierOverlayRun(t, "mutation_origin_ignored.go", "^TestFrameCarrierBlankChildOriginOnly$", true)
}

func TestCDPFrameCarrierMutationScrubFirst(t *testing.T) {
	frameCarrierOverlayRun(t, "mutation_scrub_first.go", "^TestFrameCarrierScrubOrder$", true)
}

func TestCDPFrameCarrierMutationChildEvents(t *testing.T) {
	frameCarrierOverlayRun(t, "mutation_child_events.go", "^TestFrameCarrierChildFrameNavigated$", true)
}

func TestCDPFrameCarrierMutationAncestorName(t *testing.T) {
	frameCarrierOverlayRun(t, "mutation_ancestor_name.go", "^TestFrameCarrierAncestorNameTree$", true)
}

func TestCDPFrameCarrierMutationExtensionProtected(t *testing.T) {
	frameCarrierOverlayRun(t, "mutation_extension_protected.go", "^TestFrameCarrierExtensionFrameTree$", true)
}

func TestCDPFrameCarrierMutationListingTitleOffSecondLayerHolds(t *testing.T) {
	frameCarrierOverlayRun(t, "mutation_listing_title_off.go", "^TestFrameCarrierExtensionTabTitle$", false)
}

func TestCDPFrameCarrierMutationTargetLayerRemoved(t *testing.T) {
	frameCarrierOverlayRun(t, "mutation_target_layer_removed.go", "^TestFrameCarrierExtensionTabTitle$", true)
}

func TestCDPFrameCarrierMutationTargetTypeSplitOff(t *testing.T) {
	frameCarrierOverlayRun(t, "mutation_target_type_split_off.go", "^TestFrameCarrierExtensionTabTitle$", true)
}

func TestCDPFrameCarrierMutationDepthFailOpen(t *testing.T) {
	frameCarrierOverlayRun(t, "mutation_depth_fail_open.go", "^TestFrameCarrier(DepthTree|DepthEvents|AncestryCycle)$", true)
}

func TestCDPFrameCarrierMutationEvidenceGlobal(t *testing.T) {
	frameCarrierOverlayRun(t, "mutation_evidence_global.go", "^TestFrameCarrierEvidenceScopedToPage$", true)
}
