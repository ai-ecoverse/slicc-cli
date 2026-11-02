package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestNPMPublishRecoversTransparencyLogConflicts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake npm uses a POSIX executable")
	}
	node := requireNode(t)
	for _, c := range []struct {
		name, message, visible    string
		failures, attempts, views int
		dryRun, success           bool
	}{
		{"success", "", "", 0, 1, 1, false, true},
		{"already", "", "always", 0, 0, 1, false, true},
		{"accepted", "npm error TLOG_CREATE_ENTRY_ERROR", "after", 1, 1, 2, false, true},
		{"retry", "npm error TLOG_CREATE_ENTRY_ERROR", "", 1, 2, 2, false, true},
		{"http409", "sigstore transparency log answered 409", "", 1, 2, 2, false, true},
		{"http409first", "409 Conflict from Rekor", "after", 1, 1, 2, false, true},
		{"exhausted", "TLOG_CREATE_ENTRY_ERROR", "", 3, 3, 4, false, false},
		{"wrongVersion", "TLOG_CREATE_ENTRY_ERROR", "wrong", 3, 3, 4, false, false},
		{"otherError", "npm error E403 forbidden", "after", 1, 1, 1, false, false},
		{"other409", "npm error E409 package version conflict", "after", 1, 1, 1, false, false},
		{"dryRun", "TLOG_CREATE_ENTRY_ERROR", "after", 1, 1, 0, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			pkgDir := filepath.Join(dir, "packages", "wrapper")
			if err := os.MkdirAll(pkgDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"@ai-ecoverse/slicc","version":"1.58.1"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			message, _ := json.Marshal(c.message)
			visible, _ := json.Marshal(c.visible)
			fake := "#!" + node + `
import { appendFileSync, existsSync, readFileSync, writeFileSync } from 'node:fs';
const args = process.argv.slice(2);
const log = process.env.FAKE_NPM_LOG;
appendFileSync(log, JSON.stringify(args) + '\n');
const state = log + '.state';
const attempts = existsSync(state) ? Number(readFileSync(state, 'utf8')) : 0;
if (args[0] === '--version') console.log('11.5.1');
else if (args[0] === 'view') {
  const visible = ` + string(visible) + `;
  if (visible === 'always' || (attempts > 0 && visible === 'after')) console.log('1.58.1');
  else if (visible === 'wrong') console.log('1.57.2');
  else process.exit(1);
} else if (args[0] === 'publish') {
  writeFileSync(state, String(attempts + 1));
  if (attempts < ` + strconv.Itoa(c.failures) + `) {
    console.error(` + string(message) + `);
    process.exit(1);
  }
  console.log('published fixture');
} else process.exit(99);
`
			if err := os.WriteFile(filepath.Join(dir, "npm"), []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, "calls")
			env := publishEnv(t, "FAKE_NPM_LOG="+log)
			for i, kv := range env {
				if strings.HasPrefix(kv, "PATH=") {
					env[i] = "PATH=" + dir + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
				}
			}
			var args []string
			if c.dryRun {
				args = append(args, "--dry-run")
			}
			r := publish(t, node, filepath.Dir(pkgDir), env, args...)
			if (r.code == 0) != c.success || strings.Contains(r.stdout, "npm-publish: PASS") != c.success {
				t.Fatalf("publish result: %+v", r)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			attempts, views := 0, 0
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var call []string
				if err := json.Unmarshal([]byte(line), &call); err != nil {
					t.Fatal(err)
				}
				switch call[0] {
				case "publish":
					attempts++
					flag := "--provenance"
					if c.dryRun {
						flag = "--dry-run"
					}
					if strings.Join(call, " ") != "publish "+pkgDir+" --access public --tag latest "+flag {
						t.Fatalf("publish arguments: %v", call)
					}
				case "view":
					views++
					if strings.Join(call, " ") != "view @ai-ecoverse/slicc@1.58.1 version" {
						t.Fatalf("view arguments: %v", call)
					}
				}
			}
			if attempts != c.attempts || views != c.views {
				t.Fatalf("publishes=%d views=%d, want %d %d: %+v", attempts, views, c.attempts, c.views, r)
			}
			if c.attempts > 1 && !strings.Contains(r.stdout, "in 5 s after the transparency log conflict") {
				t.Fatalf("missing first backoff: %+v", r)
			}
			if c.attempts == 3 && !strings.Contains(r.stdout, "in 10 s after the transparency log conflict") {
				t.Fatalf("missing second backoff: %+v", r)
			}
		})
	}
}
