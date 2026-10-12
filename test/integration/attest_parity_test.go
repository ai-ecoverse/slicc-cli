package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/attest"
)

const parityEnv = "SLICC_ATTEST_PARITY"

func ghJSON(t *testing.T, out any, args ...string) {
	t.Helper()
	b, err := exec.Command("gh", args...).Output()
	if err != nil {
		t.Fatalf("gh %v: %v", args, err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("gh %v: %v", args, err)
	}
}

func ourVerdict(t *testing.T, root *attest.TrustedRoot, file string) error {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	b, err := exec.Command("gh", "api", "repos/"+sliccRelease.Repo+"/attestations/sha256:"+digest).Output()
	if err != nil {
		return err
	}
	var list struct {
		Attestations []struct {
			Bundle json.RawMessage `json:"bundle"`
		} `json:"attestations"`
	}
	if err := json.Unmarshal(b, &list); err != nil || len(list.Attestations) == 0 {
		return os.ErrNotExist
	}
	var last error
	for _, a := range list.Attestations {
		if last = attest.Verify(a.Bundle, digest, sliccRelease, root); last == nil {
			return nil
		}
	}
	return last
}

func ghVerdict(file string) error {
	return exec.Command("gh", "attestation", "verify", file, "--repo", sliccRelease.Repo, "--signer-workflow", sliccRelease.Repo+"/.github/workflows/release.yml").Run()
}

func TestAttestParityWithGH(t *testing.T) {
	if os.Getenv(parityEnv) == "" {
		t.Skip("set " + parityEnv + "=1 to compare with gh attestation verify on the latest release (network)")
	}
	requireBinary(t, "gh")
	root, err := attest.DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	var rel struct {
		TagName string `json:"tagName"`
	}
	ghJSON(t, &rel, "release", "view", "--repo", sliccRelease.Repo, "--json", "tagName")
	dir := t.TempDir()
	if out, err := exec.Command("gh", "release", "download", rel.TagName, "--repo", sliccRelease.Repo, "-p", "slicc-linux-amd64", "-D", dir).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	file := filepath.Join(dir, "slicc-linux-amd64")
	ours, theirs := ourVerdict(t, root, file), ghVerdict(file)
	if ours != nil || theirs != nil {
		t.Fatalf("%s: ours %v, gh %v", rel.TagName, ours, theirs)
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte{0})
	_ = f.Close()
	if ours, theirs := ourVerdict(t, root, file), ghVerdict(file); ours == nil || theirs == nil {
		t.Fatalf("a changed file: ours %v, gh %v", ours, theirs)
	}
}
