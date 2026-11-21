package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func whatwgURLTableRun(t *testing.T, mutant map[string]string) (string, error) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/whatwgurl_test.go"): filepath.Join(root, "test/integration/testdata/whatwgurl/regression.go"),
	}
	if mutant != nil {
		source := filepath.Join(root, "internal/protect/protect.go")
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for from, to := range mutant {
			if n := strings.Count(text, from); n != 1 {
				t.Fatalf("mutant anchor %q appears %d times in protect.go", from, n)
			}
			text = strings.Replace(text, from, to, 1)
		}
		mutated := filepath.Join(scratch, "protect.go")
		if err := os.WriteFile(mutated, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[source] = mutated
	}
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(scratch, "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", "^TestWhatwgURLDecisionTable$", "./internal/caps/cdp")
	command.Dir = root
	command.Env = append(os.Environ(), "SLICC_FIXTURE_WRITE=", "SLICC_WHATWG_INPUTS=", "SLICC_WHATWG_TABLE=")
	output, err := command.CombinedOutput()
	return string(output), err
}

func whatwgURLTableRows(t *testing.T) map[string][]string {
	t.Helper()
	f, err := os.Open("testdata/whatwgurl/decisions.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rows := map[string][]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 3)
		if len(parts) != 3 {
			t.Fatalf("bad row %q", sc.Text())
		}
		rows[parts[0]] = append(rows[parts[0]], parts[1])
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestProtectURLDecisionTable(t *testing.T) {
	rows := whatwgURLTableRows(t)
	for _, cat := range []string{"ace", "std3", "unicode15", "utf8", "bracket", "canonical"} {
		if len(rows[cat]) == 0 {
			t.Fatalf("decision table has no %s rows", cat)
		}
	}
	if output, err := whatwgURLTableRun(t, nil); err != nil {
		t.Fatalf("URL decision table: %v\n%s", err, output)
	}
}

func TestProtectURLDecisionTableCatchesHostCheckMutants(t *testing.T) {
	rows := whatwgURLTableRows(t)
	mismatch := regexp.MustCompile(`mismatch (\S+) ("(?:[^"\\]|\\.)*")`)
	for _, c := range []struct {
		name   string
		mutant map[string]string
		cats   []string
	}{
		{"strict host check removed", map[string]string{" || !strictHost(raw, u)": ""}, []string{"ace", "std3", "unicode15"}},
		{"invalid UTF-8 kept", map[string]string{"\tif !utf8.ValidString(raw) {\n\t\traw = string([]rune(raw))\n\t}\n": ""}, []string{"utf8"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			output, err := whatwgURLTableRun(t, c.mutant)
			if err == nil {
				t.Fatalf("the decision table passes with the mutant\n%s", output)
			}
			got := map[string]string{}
			for _, m := range mismatch.FindAllStringSubmatch(output, -1) {
				if _, err := strconv.Unquote(m[2]); err != nil {
					t.Fatalf("mismatch line with bad input %q", m[2])
				}
				got[m[2]] = m[1]
			}
			want := 0
			for _, cat := range c.cats {
				for _, in := range rows[cat] {
					want++
					if got[in] != cat {
						t.Errorf("%s row %s still passes", cat, in)
					}
				}
			}
			if len(got) != want {
				t.Errorf("%d rows fail with the mutant, want exactly the %d rows of %v", len(got), want, c.cats)
			}
		})
	}
}
