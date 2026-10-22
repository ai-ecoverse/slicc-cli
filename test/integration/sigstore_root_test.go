package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sigstoreRoot(t *testing.T, node string, args ...string) (string, int) {
	t.Helper()
	out, err := exec.Command(node, append([]string{"../../scripts/sigstore-root.mjs"}, args...)...).CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func TestTheEmbeddedSigstoreTrustedRootIsCurrent(t *testing.T) {
	node := requireNode(t)
	out, code := sigstoreRoot(t, node)
	switch code {
	case 0:
		if !strings.Contains(out, "trusted_root.json is current") {
			t.Fatalf("sigstore-root exited 0 without saying the root is current:\n%s", out)
		}
	case 3:
		t.Skipf("Sigstore's TUF repository didn't answer:\n%s", out)
	case 1:
		t.Fatalf("Sigstore rotated its trusted root; re-embed internal/attest/trusted_root.json as README.md's Rotating the trusted root says:\n%s", out)
	default:
		t.Fatalf("sigstore-root exited %d:\n%s", code, out)
	}
}

func TestSigstoreRootTellsAnOutageFromARotation(t *testing.T) {
	node := requireNode(t)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer down.Close()
	if out, code := sigstoreRoot(t, node, "--repo", down.URL); code != 3 {
		t.Fatalf("a TUF repository that answers 502: exit %d, want 3\n%s", code, out)
	}
	file := filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(file, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	live := []byte(`{"live":true}`)
	sum := sha256Hex(live)
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/timestamp.json":
			_, _ = w.Write([]byte(`{"signed":{"meta":{"snapshot.json":{"version":7}}}}`))
		case "/7.snapshot.json":
			_, _ = w.Write([]byte(`{"signed":{"meta":{"targets.json":{"version":9}}}}`))
		case "/9.targets.json":
			_, _ = w.Write([]byte(`{"signed":{"targets":{"trusted_root.json":{"hashes":{"sha256":"` + sum + `"}}}}}`))
		case "/targets/" + sum + ".trusted_root.json":
			_, _ = w.Write(live)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repo.Close()
	if out, code := sigstoreRoot(t, node, "--repo", repo.URL, "--file", file); code != 1 || !strings.Contains(out, "trusted_root.json changed") {
		t.Fatalf("a rotated root: exit %d, want 1\n%s", code, out)
	}
	if out, code := sigstoreRoot(t, node, "--repo", repo.URL, "--file", file, "--write"); code != 0 {
		t.Fatalf("--write: exit %d\n%s", code, out)
	}
	if out, code := sigstoreRoot(t, node, "--repo", repo.URL, "--file", file); code != 0 || !strings.Contains(out, "is current") {
		t.Fatalf("after --write: exit %d\n%s", code, out)
	}
}
