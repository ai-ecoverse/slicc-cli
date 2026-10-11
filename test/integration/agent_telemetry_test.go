package integration

import (
	"os"
	"strings"
	"testing"
)

func TestAgentTelemetrySendsTheVerbOnly(t *testing.T) {
	col := newCollector(t)
	bin := buildVersion(t, "1.0.0", t.TempDir())
	env := append(os.Environ(), append(col.env(), "SLICC_NO_UPDATE_CHECK=1")...)
	cdp := "http://" + closedAddr(t)
	for _, args := range [][]string{{"prompt", "--cdp", cdp, "s3cr3t", "prompt"}, {"watch", "--cdp", cdp}, {"abort", "--cdp", cdp}} {
		before := len(col.got())
		r := runBin(t, env, bin, args...)
		if r.code != 1 {
			t.Fatalf("%v: %+v", args, r)
		}
		var sources []string
		for _, b := range col.got()[before:] {
			if b.Checkpoint == "enter" {
				sources = append(sources, b.Source)
			}
		}
		if len(sources) != 1 || sources[0] != args[0] {
			t.Fatalf("%v: enter sources %v", args, sources)
		}
	}
	for _, b := range col.got() {
		if strings.Contains(b.raw, "s3cr3t") {
			t.Fatalf("leaked the prompt: %s", b.raw)
		}
	}
}
