package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stageSliccy(t *testing.T, node, version string) string {
	t.Helper()
	return stage(t, node, binary, "--name", "sliccy", "--launcher-only", "--version", version, "--deps-version", "1.2.3-test")
}

func publishEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	env := []string{}
	for _, kv := range isolatedEnv(t) {
		key := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		if key == "SLICCY_NEXT_PUBLISH" || key == "NODE_AUTH_TOKEN" || key == "NPM_TOKEN" || strings.HasPrefix(key, "NPM_CONFIG_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, append([]string{"npm_config_registry=http://127.0.0.1:1/", "npm_config_fetch_retries=0"}, extra...)...)
}

func publish(t *testing.T, node, dir string, env []string, args ...string) result {
	t.Helper()
	return runBin(t, env, node, append([]string{"../../scripts/npm-publish.mjs", "--dir", dir}, args...)...)
}

func TestSliccyNextIsStagedAgainstTheReleasePackages(t *testing.T) {
	node := requireNode(t)
	staged := stageSliccy(t, node, "7.0.0-next.1.2.3-test")
	entries, err := os.ReadDir(staged)
	if err != nil || len(entries) != 1 {
		t.Fatalf("staged %v (%v), want only the launcher", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(staged, "slicc", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Name     string            `json:"name"`
		Version  string            `json:"version"`
		Bin      map[string]string `json:"bin"`
		Optional map[string]string `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "sliccy" || pkg.Version != "7.0.0-next.1.2.3-test" || pkg.Bin["sliccy"] != "bin/slicc.js" || len(pkg.Bin) != 1 {
		t.Fatalf("%+v", pkg)
	}
	if pkg.Optional["@ai-ecoverse/slicc-"+target()] != "1.2.3-test" || len(pkg.Optional) != 1 {
		t.Fatalf("optionalDependencies %v", pkg.Optional)
	}
	readme, err := os.ReadFile(filepath.Join(staged, "slicc", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"preview of the new SLICC, seven", "npx sliccy@next", "npx sliccy@6"} {
		if !strings.Contains(string(readme), want) {
			t.Fatalf("README lacks %q:\n%s", want, readme)
		}
	}
	modules := filepath.Join(t.TempDir(), "node_modules")
	install(t, staged, modules, false)
	platform := stage(t, node, binary)
	if err := os.CopyFS(filepath.Join(modules, "@ai-ecoverse", "slicc-"+target()), os.DirFS(filepath.Join(platform, "slicc-"+target()))); err != nil {
		t.Fatal(err)
	}
	if r := launcher(t, node, modules, isolatedEnv(t), "--version"); r.code != 0 || r.stdout != "slicc 1.2.3-test\n" {
		t.Fatalf("%+v", r)
	}
}

func TestSliccyOnlyPublishesToNext(t *testing.T) {
	node := requireNode(t)
	good := stageSliccy(t, node, "7.0.0-next.1.2.3")
	bad := stageSliccy(t, node, "7.1.0-next.1.2.3")
	refusals := []struct {
		dir  string
		args []string
		want string
	}{
		{good, []string{"--dry-run"}, "with the tag latest; it only goes to next"},
		{good, []string{"--dry-run", "--tag", "latest"}, "with the tag latest; it only goes to next"},
		{good, []string{"--dry-run", "--tag", "beta"}, "with the tag beta; it only goes to next"},
		{good, []string{"--tag", "latest"}, "with the tag latest; it only goes to next"},
		{bad, []string{"--dry-run", "--tag", "next"}, "sliccy@7.1.0-next.1.2.3; its version must match"},
		{bad, []string{"--tag", "next"}, "its version must match"},
	}
	for _, c := range refusals {
		r := publish(t, node, c.dir, publishEnv(t, "SLICCY_NEXT_PUBLISH=true"), c.args...)
		if r.code == 0 || !strings.Contains(r.stderr, c.want) || strings.Contains(r.stdout+r.stderr, "Publishing to") {
			t.Fatalf("%v: %+v", c.args, r)
		}
	}
	r := publish(t, node, good, publishEnv(t), "--tag", "next")
	if r.code != 0 || !strings.Contains(r.stdout, "skipping sliccy@7.0.0-next.1.2.3; set the repository variable SLICCY_NEXT_PUBLISH") {
		t.Fatalf("gated off: %+v", r)
	}
	r = publish(t, node, good, publishEnv(t), "--tag", "next", "--dry-run")
	if r.code != 0 || !strings.Contains(r.stderr, "with tag next") || !strings.Contains(r.stderr, "(dry-run)") {
		t.Fatalf("dry run: %+v", r)
	}
}
