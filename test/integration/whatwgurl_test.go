package integration

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const unicode15TableSHA256 = "3f6d2d8efe2ca082a5d9a089bbba190b1c07d45cbfd408be3a282938707ec3a8"

type protectOverlayRun struct {
	pkg     string
	file    string
	regress string
	extra   map[string]string
	run     string
	timeout time.Duration
}

var (
	whatwgURLTable = protectOverlayRun{
		pkg: "./internal/caps/cdp", file: "internal/caps/cdp/whatwgurl_test.go",
		regress: "test/integration/testdata/whatwgurl/regression.go", run: "^TestWhatwgURLDecisionTable$",
	}
	protectHostRunes = protectOverlayRun{
		pkg: "./internal/protect", file: "internal/protect/hostrunes_test.go",
		regress: "test/integration/testdata/whatwgurl/hostrunes.go", run: "^TestHostRunes",
	}
)

func whatwgURLTableRun(t *testing.T, mutant map[string]string, files map[string]string) (string, error) {
	t.Helper()
	return protectOverlay(t, whatwgURLTable, mutant, files)
}

func protectOverlay(t *testing.T, target protectOverlayRun, mutant map[string]string, files map[string]string) (string, error) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{
		filepath.Join(root, target.file): filepath.Join(root, target.regress),
	}
	for dest, src := range target.extra {
		replacements[filepath.Join(root, dest)] = filepath.Join(root, src)
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
	for name, body := range files {
		target := filepath.Join(root, name)
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("mutant replaces %s: %v", name, err)
		}
		mutated := filepath.Join(scratch, filepath.Base(name))
		if err := os.WriteFile(mutated, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[target] = mutated
	}
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(scratch, "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	limit := target.timeout
	if limit <= 0 {
		limit = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(t.Context(), limit)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-v", "-run", target.run, target.pkg)
	command.Dir = root
	command.Env = append(os.Environ(), "SLICC_FIXTURE_WRITE=", "SLICC_WHATWG_INPUTS=", "SLICC_WHATWG_TABLE=")
	output, err := command.CombinedOutput()
	if !strings.Contains(string(output), "=== RUN   Test") {
		t.Fatalf("%s ran no test with the overlay: %v\n%s", target.pkg, err, output)
	}
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
	for _, cat := range []string{"ace", "std3", "unicode15", "notequal", "nonchar", "utf8", "bracket", "canonical"} {
		if len(rows[cat]) == 0 {
			t.Fatalf("decision table has no %s rows", cat)
		}
	}
	if output, err := whatwgURLTableRun(t, nil, nil); err != nil {
		t.Fatalf("URL decision table: %v\n%s", err, output)
	}
}

func TestProtectURLDecisionTableCatchesHostCheckMutants(t *testing.T) {
	rows := whatwgURLTableRows(t)
	mismatch := regexp.MustCompile(`mismatch (\S+) ("(?:[^"\\]|\\.)*")`)
	for _, c := range []struct {
		name   string
		mutant map[string]string
		files  map[string]string
		cats   []string
		keep   []string
	}{
		{"strict host check removed", map[string]string{" || !strictHost(raw, u)": ""}, nil, []string{"ace", "std3", "unicode15", "notequal", "nonchar"}, nil},
		{"invalid UTF-8 kept", map[string]string{"\tif !utf8.ValidString(raw) {\n\t\traw = string([]rune(raw))\n\t}\n": ""}, nil, []string{"utf8"}, nil},
		{"Unicode 15 table replaced by the toolchain's", nil, map[string]string{"internal/protect/unicode15.go": "package protect\n\nimport (\n\t\"unicode\"\n\n\t\"golang.org/x/text/unicode/rangetable\"\n)\n\nvar unicode15 = rangetable.Assigned(unicode.Version)\n"}, []string{"unicode15"}, nil},
		{"IDNA validation removed from the host check", map[string]string{"\tif _, err := idnaProfile.ToASCII(host); err != nil {\n\t\treturn false\n\t}\n": ""}, nil, nil, []string{"nonchar", "unicode15"}},
		{"noncharacter rejection removed from the host check", map[string]string{"noncharacter(r) || ": ""}, nil, nil, []string{"nonchar"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			output, err := whatwgURLTableRun(t, c.mutant, c.files)
			if err == nil && c.cats != nil {
				t.Fatalf("the decision table passes with the mutant\n%s", output)
			}
			got := map[string]string{}
			for _, m := range mismatch.FindAllStringSubmatch(output, -1) {
				if _, err := strconv.Unquote(m[2]); err != nil {
					t.Fatalf("mismatch line with bad input %q", m[2])
				}
				got[m[2]] = m[1]
			}
			for _, cat := range c.keep {
				for _, in := range rows[cat] {
					if got[in] != "" {
						t.Errorf("%s row %s fails, but another layer of the host check refuses it", cat, in)
					}
				}
			}
			if c.cats == nil {
				return
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

func TestProtectUnicode15TableIsFrozen(t *testing.T) {
	data, err := os.ReadFile("../../internal/protect/unicode15.go")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != unicode15TableSHA256 {
		t.Fatalf("internal/protect/unicode15.go changed (sha256 %s, want %s). The Unicode 15.0 table is frozen: restore it with `node scripts/unicode15.mjs > internal/protect/unicode15.go`. Moving it to another Unicode version is a reviewed change: point scripts/unicode15.mjs at that version's DerivedAge.txt and its sha256, regenerate the file, update unicode15TableSHA256, rewrite test/integration/testdata/whatwgurl/decisions.txt with SLICC_FIXTURE_WRITE=1, and have the table's diff reviewed", got, unicode15TableSHA256)
	}
	fset := token.NewFileSet()
	for _, name := range []string{"protect.go", "credential.go", "unicode15.go"} {
		f, err := parser.ParseFile(fset, filepath.Join("../../internal/protect", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if path := strings.Trim(imp.Path.Value, `"`); strings.HasPrefix(path, "golang.org/x/text/unicode") {
				t.Errorf("internal/protect/%s imports %s: the host check's Unicode table must not follow x/text", name, path)
			}
		}
	}
}

func TestProtectHostCheckRefusesNoncharactersItself(t *testing.T) {
	if output, err := protectOverlay(t, protectHostRunes, nil, nil); err != nil {
		t.Fatalf("host check on noncharacters: %v\n%s", err, output)
	}
	output, err := protectOverlay(t, protectHostRunes, map[string]string{"noncharacter(r) || ": ""}, nil)
	if err == nil {
		t.Fatalf("the noncharacter rows pass without the host check's own rejection\n%s", output)
	}
	blocks := []string{"U+FDD0..U+FDEF"}
	for plane := range 17 {
		blocks = append(blocks, fmt.Sprintf("U+%X..U+%X", plane<<16|0xFFFE, plane<<16|0xFFFF))
	}
	for _, b := range blocks {
		if !strings.Contains(output, "noncharacter block "+b+" accepted") {
			t.Errorf("block %s still passes without the host check's own rejection", b)
		}
	}
}
