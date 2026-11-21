package cdp

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

type urlTableSet struct {
	name string
	o    *protect.Origins
}

func urlTableSets() []urlTableSet {
	return []urlTableSet{
		{"nil", nil},
		{"vectors", protect.MustNew([]string{"http://127.0.0.1:5710", "https://seven.example.test"})},
		{"extra", protect.MustNew([]string{"https://seven.example.test", "http://127.0.0.1:5710", "https://Extra.Example:8443", "http://[::1]:9000", "https://xn--bcher-kva.example", "http://10.0.0.1"})},
	}
}

func urlTableMatch(pattern, url string) bool {
	var rec func(pi, si int) bool
	rec = func(pi, si int) bool {
		for pi < len(pattern) || si < len(url) {
			if pi < len(pattern) && pattern[pi] == '*' {
				for k := si; k <= len(url); k++ {
					if rec(pi+1, k) {
						return true
					}
				}
				return false
			}
			if pi >= len(pattern) || si >= len(url) || pattern[pi] != url[si] {
				return false
			}
			pi++
			si++
		}
		return true
	}
	return rec(0, 0)
}

func urlTableFetchCover(o *protect.Origins, url string) bool {
	can, ok := protect.Canonical(url)
	if url == "" || !ok || can == "" {
		return false
	}
	for _, p := range o.DocumentPatterns() {
		if urlTableMatch(p, can) {
			return true
		}
	}
	return false
}

func urlTableAuthority(s string) string {
	i := strings.Index(s, "://")
	if i < 0 {
		return ""
	}
	h := s[i+3:]
	if j := strings.IndexAny(h, "/?#\\"); j >= 0 {
		h = h[:j]
	}
	if k := strings.LastIndexByte(h, '@'); k >= 0 {
		h = h[k+1:]
	}
	return h
}

func urlTableBits(bs ...bool) string {
	var b strings.Builder
	for _, v := range bs {
		if v {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

func urlTableCut(g *guard, s string) string {
	out, changed := g.cutEmbedded(s)
	h := fnv.New32a()
	_, _ = h.Write([]byte(out))
	return fmt.Sprintf("%s:%08x", urlTableBits(changed), h.Sum32())
}

func urlTableDecisions(in string) string {
	can, ok := protect.Canonical(in)
	var b strings.Builder
	fmt.Fprintf(&b, "canonical=%q top=%s scheme=%q host=%q redact=%q", can,
		urlTableBits(ok, protect.Parses(in), protect.Unparseable(in), protect.Inheriting(in), protect.ExtensionScheme(in),
			protect.CredentialURL(in), protect.CredentialSite(in), protect.CredentialStorageKey(in)),
		protect.Scheme(in), protect.Host(in), protect.Redact(in))
	auth := urlTableAuthority(in)
	if auth != "" {
		h, settled := canonicalAuthority(auth)
		fmt.Fprintf(&b, " authority=%q:%s", h, urlTableBits(settled))
	}
	for _, set := range urlTableSets() {
		o := set.o
		g := &guard{o: o}
		fmt.Fprintf(&b, " %s=%s,%s,%s", set.name,
			urlTableBits(o.URL(in), o.Loaded(in), o.Target(in, false, protect.NoOpener), o.Target(in, true, protect.NoOpener),
				o.Kept(in), o.SevenOrVaultHost(in), o.KeptInherited("about:blank", in), o.ExtensionFrame(in), o.MayNameHost(in),
				o.StorageKey(in), urlTableFetchCover(o, in), g.ordinaryEmbedded(in), auth != "" && g.mayNameAuthority(auth)),
			urlTableCut(g, "pre "+in+" post"),
			urlTableCut(g, "q="+strings.ReplaceAll(strings.ReplaceAll(in, ":", "%3A"), "/", "%2F")))
	}
	return b.String()
}

type urlTableRow struct {
	cat  string
	in   string
	want string
}

func readURLTable(t *testing.T, path string, withWant bool) []urlTableRow {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var rows []urlTableRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 3)
		if len(parts) < 2 || withWant && len(parts) < 3 {
			t.Fatalf("%s: bad row %q", path, sc.Text())
		}
		in, err := strconv.Unquote(parts[1])
		if err != nil {
			t.Fatalf("%s: bad input %q: %v", path, parts[1], err)
		}
		row := urlTableRow{cat: parts[0], in: in}
		if len(parts) == 3 {
			row.want = parts[2]
		}
		rows = append(rows, row)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestWhatwgURLDecisionTable(t *testing.T) {
	path := os.Getenv("SLICC_WHATWG_TABLE")
	if path == "" {
		path = "../../../test/integration/testdata/whatwgurl/decisions.txt"
	}
	if inputs := os.Getenv("SLICC_WHATWG_INPUTS"); inputs != "" || os.Getenv("SLICC_FIXTURE_WRITE") == "1" {
		src := path
		if inputs != "" {
			src = inputs
		}
		rows := readURLTable(t, src, false)
		var b strings.Builder
		for _, r := range rows {
			fmt.Fprintf(&b, "%s\t%s\t%s\n", r.cat, strconv.Quote(r.in), urlTableDecisions(r.in))
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d rows to %s", len(rows), path)
		return
	}
	rows := readURLTable(t, path, true)
	if len(rows) == 0 {
		t.Fatal("empty decision table")
	}
	mismatches := 0
	for _, r := range rows {
		if got := urlTableDecisions(r.in); got != r.want {
			mismatches++
			t.Errorf("mismatch %s %s\n got  %s\n want %s", r.cat, strconv.Quote(r.in), got, r.want)
		}
	}
	t.Logf("%d rows, %d mismatches", len(rows), mismatches)
}
