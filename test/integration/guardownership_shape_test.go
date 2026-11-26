package integration

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFrameOwnershipProbeAuditShapeYieldsIncomplete(t *testing.T) {
	tmpdir, err := os.MkdirTemp("", "ownership-shape-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpdir) }()
	cmd := exec.Command(requireNode(t), filepath.Join("testdata", "guard", "ownership_ledger_shape.mjs"))
	cmd.Env = append(os.Environ(), "TMPDIR="+tmpdir)
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			out = append(out, exit.Stderr...)
		}
		t.Fatalf("ownership ledger shape: %v\n%s", err, out)
	}
	var got struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(out, &got); err != nil || !got.OK {
		t.Fatalf("ownership ledger shape reply: %s (%v)", out, err)
	}
}
