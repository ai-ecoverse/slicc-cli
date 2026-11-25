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

func legacyInterceptionOverlayRun(t *testing.T, mutant, run string) (string, error) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/legacy_interception_test.go"): filepath.Join(root, "test/integration/testdata/legacyinterception/regression.go"),
	}
	if mutant != "" {
		replacements[filepath.Join(root, "internal/caps/cdp/legacy_interception_switches.go")] = filepath.Join(root, "test/integration/testdata/legacyinterception", mutant)
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
	return string(output), err
}

func TestCDPLegacyInterceptionEdges(t *testing.T) {
	if output, err := legacyInterceptionOverlayRun(t, "", "^TestLegacyInterception"); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

func TestCDPLegacyInterceptionMutants(t *testing.T) {
	for _, m := range []struct{ mutant, run, want string }{
		{"mutation_setup.go", "^TestLegacyInterceptionClientRefused$/^page$/^handed$/^set-enable$", `admitted and forwarded to Chrome: {"id":40,"method":"Network.setRequestInterception"`},
		{"mutation_setup.go", "^TestLegacyInterceptionClientRefused$/^browser$/^guessed$/^set-disable$", `admitted and forwarded to Chrome: {"id":42,"method":"Network.setRequestInterception"`},
		{"mutation_continue.go", "^TestLegacyInterceptionClientRefused$/^page$/^handed$/^continue-headers$", `admitted and forwarded to Chrome: {"id":44,"method":"Network.continueInterceptedRequest"`},
		{"mutation_continue.go", "^TestLegacyInterceptionClientRefused$/^root$/^handed$/^continue-id$", `admitted and forwarded to Chrome: {"id":43,"method":"Network.continueInterceptedRequest"`},
		{"mutation_continue.go", "^TestLegacyInterceptionClientRefused$/^worker$/^guessed$/^continue-empty$", `admitted and forwarded to Chrome: {"id":52,"method":"Network.continueInterceptedRequest"`},
		{"mutation_continue.go", "^TestLegacyInterceptionBecomesProtected$/^locked-then-refused$", "slicc didn't pass this paused request to the page"},
		{"mutation_bodies.go", "^TestLegacyInterceptionClientRefused$/^page$/^handed$/^body$", `admitted and forwarded to Chrome: {"id":53,"method":"Network.getResponseBodyForInterception"`},
		{"mutation_bodies.go", "^TestLegacyInterceptionClientRefused$/^root$/^guessed$/^stream$", `admitted and forwarded to Chrome: {"id":54,"method":"Network.takeResponseBodyForInterceptionAsStream"`},
		{"mutation_bodies.go", "^TestLegacyInterceptionBodyOfGuardPause$/^page$", `admitted and forwarded to Chrome: {"id":103,"method":"Network.getResponseBodyForInterception"`},
	} {
		t.Run(m.mutant+m.run, func(t *testing.T) {
			output, err := legacyInterceptionOverlayRun(t, m.mutant, m.run)
			if err == nil || !strings.Contains(output, "--- FAIL: TestLegacyInterception") || !strings.Contains(output, m.want) {
				t.Fatalf("mutant %s must fail %s with %q\n%s", m.mutant, m.run, m.want, output)
			}
		})
	}
}

func TestCDPLegacyInterceptionMutantsKeepTwins(t *testing.T) {
	for _, mutant := range []string{"mutation_setup.go", "mutation_continue.go", "mutation_bodies.go"} {
		t.Run(mutant, func(t *testing.T) {
			if output, err := legacyInterceptionOverlayRun(t, mutant, "^TestLegacyInterception(StockFetchTranscriptUnchanged|ProtectedTwin|GuardOwnFallbackUnchanged|GuardOverflowContinueUnchanged|UnflattenedWrapperKeepsItsReason|EarlyFormsEverywhere)$"); err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
		})
	}
}
