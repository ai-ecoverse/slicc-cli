package integration

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const fixtureWriteEnv = "SLICC_FIXTURE_WRITE"

func nativeFixture(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "native", name)
	if os.Getenv(fixtureWriteEnv) != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with %s=1 to write it)", err, fixtureWriteEnv)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("%s is out of date (run with %s=1 to rewrite it):\n--- want\n%s\n--- got\n%s", path, fixtureWriteEnv, want, got)
	}
}

func indentJSON(t *testing.T, line string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(strings.TrimSpace(line)), "", "  "); err != nil {
		t.Fatalf("not JSON: %v: %q", err, line)
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}

type capsDoc struct {
	Schema   int    `json:"schema"`
	Version  string `json:"version"`
	Contract int    `json:"contract"`
	Commands map[string]struct {
		JSON       string `json:"json"`
		Vocabulary string `json:"vocabulary"`
	} `json:"commands"`
	Events struct {
		Schema int      `json:"schema"`
		Names  []string `json:"names"`
	} `json:"events"`
	Agent struct {
		Events []string `json:"events"`
		Own    []string `json:"own"`
	} `json:"agent"`
	Control struct {
		V   int      `json:"v"`
		Ops []string `json:"ops"`
	} `json:"control"`
}

func readCapabilities(t *testing.T) (capsDoc, string) {
	t.Helper()
	out, errOut, code := slicc(t, "capabilities", "--json")
	if code != 0 || errOut != "" || strings.Count(out, "\n") != 1 {
		t.Fatalf("code %d, stderr %q, stdout %q", code, errOut, out)
	}
	var c capsDoc
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatal(err)
	}
	return c, out
}

func parseGoDir(t *testing.T, dir string) []*ast.File {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	return files
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

func stringConsts(files []*ast.File, keep func(name string, typ ast.Expr) bool) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST {
				continue
			}
			for _, spec := range g.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if i >= len(vs.Values) || !keep(n.Name, vs.Type) {
						continue
					}
					if s, ok := stringLit(vs.Values[i]); ok {
						out[n.Name] = s
					}
				}
			}
		}
	}
	return out
}

func switchCases(files []*ast.File, fn string, tag string) [][]string {
	var out [][]string
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != fn || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				sw, ok := n.(*ast.SwitchStmt)
				if !ok || sw.Tag == nil || exprString(sw.Tag) != tag {
					return true
				}
				for _, st := range sw.Body.List {
					var lits []string
					for _, e := range st.(*ast.CaseClause).List {
						if s, ok := stringLit(e); ok {
							lits = append(lits, s)
						}
					}
					if len(lits) > 0 {
						out = append(out, lits)
					}
				}
				return true
			})
		}
	}
	return out
}

func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	case *ast.IndexExpr:
		return exprString(v.X) + "[" + exprString(v.Index) + "]"
	case *ast.BasicLit:
		return v.Value
	}
	return ""
}

func emittedEvents(files []*ast.File, kinds map[string]string) map[string]bool {
	value := func(e ast.Expr) (string, bool) {
		if s, ok := stringLit(e); ok {
			return s, true
		}
		if sel, ok := e.(*ast.SelectorExpr); ok && exprString(sel.X) == "flow" {
			s, ok := kinds[sel.Sel.Name]
			return s, ok
		}
		return "", false
	}
	out := map[string]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key := exprString(kv.Key)
			if key != "Event" && key != `"event"` {
				return true
			}
			if s, ok := value(kv.Value); ok {
				out[s] = true
			}
			return true
		})
	}
	return out
}

var dashboardOnly = []string{"serving", "stream-open", "stream-close", "dropped", "status", "kernel"}

func TestCapabilitiesNameEveryCommandEventAndOp(t *testing.T) {
	c, out := readCapabilities(t)
	if c.Schema != 1 || c.Contract != 1 || c.Version != "1.2.3-test" || c.Events.Schema != 1 || c.Control.V != 1 {
		t.Fatalf("header: %+v", c)
	}
	for name, cmd := range c.Commands {
		if !slices.Contains([]string{"document", "events", "none"}, cmd.JSON) {
			t.Errorf("%s: json %q", name, cmd.JSON)
		}
		if (cmd.JSON == "events") != (cmd.Vocabulary != "") {
			t.Errorf("%s: json %q with vocabulary %q", name, cmd.JSON, cmd.Vocabulary)
		}
	}
	cmdFiles := parseGoDir(t, filepath.Join("..", "..", "cmd", "slicc"))
	cases := append(switchCases(cmdFiles, "run", "args[0]"), switchCases(cmdFiles, "joinCommand", "args[1]")...)
	if len(cases) < 10 {
		t.Fatalf("found only %d command cases in cmd/slicc", len(cases))
	}
	for _, lits := range cases {
		if !slices.ContainsFunc(lits, func(s string) bool { _, ok := c.Commands[s]; return ok }) {
			t.Errorf("command %v is missing from slicc capabilities --json", lits)
		}
	}
	if _, ok := c.Commands["default"]; !ok {
		t.Error("the default flow is missing from slicc capabilities --json")
	}
	flowFiles := parseGoDir(t, filepath.Join("..", "..", "internal", "flow"))
	kinds := stringConsts(flowFiles, func(_ string, typ ast.Expr) bool { return exprString(typ) == "Kind" })
	if len(kinds) < 10 {
		t.Fatalf("found only %d flow kinds", len(kinds))
	}
	emitted := emittedEvents(append(cmdFiles, flowFiles...), kinds)
	for _, k := range kinds {
		emitted[k] = true
	}
	for name := range emitted {
		if !slices.Contains(c.Events.Names, name) && !slices.Contains(dashboardOnly, name) {
			t.Errorf("event %q is missing from slicc capabilities --json", name)
		}
	}
	for _, name := range c.Events.Names {
		if !emitted[name] {
			t.Errorf("slicc capabilities --json names event %q, which nothing emits", name)
		}
	}
	agent := stringConsts(parseGoDir(t, filepath.Join("..", "..", "internal", "agentctl")), func(name string, _ ast.Expr) bool {
		return strings.HasPrefix(name, "Event")
	})
	if len(agent) < 5 {
		t.Fatalf("found only %d agent events", len(agent))
	}
	for _, e := range agent {
		if !slices.Contains(c.Agent.Events, e) {
			t.Errorf("agent event %q is missing from slicc capabilities --json", e)
		}
	}
	if !slices.Contains(c.Agent.Own, "settled") {
		t.Errorf("agent.own %v lacks settled", c.Agent.Own)
	}
	ctlFiles := parseGoDir(t, filepath.Join("..", "..", "internal", "ctl"))
	ops := stringConsts(ctlFiles, func(name string, _ ast.Expr) bool { return strings.HasPrefix(name, "Op") })
	if len(ops) == 0 {
		t.Fatal("found no control ops")
	}
	for _, op := range ops {
		if !slices.Contains(c.Control.Ops, op) {
			t.Errorf("control op %q is missing from slicc capabilities --json", op)
		}
	}
	nativeFixture(t, "capabilities.json", indentJSON(t, out))
}

func TestCapabilitiesWithoutJSONListTheCommands(t *testing.T) {
	out, errOut, code := slicc(t, "capabilities")
	if code != 0 || errOut != "" || !strings.HasPrefix(out, "slicc 1.2.3-test, contract 1\n") || !strings.Contains(out, "capabilities  document\n") {
		t.Fatalf("code %d, stderr %q, stdout %q", code, errOut, out)
	}
	if _, errOut, code := slicc(t, "capabilities", "extra"); code != 2 || !strings.Contains(errOut, `unexpected argument "extra"`) {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}
