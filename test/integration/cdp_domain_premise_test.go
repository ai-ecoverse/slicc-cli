package integration

import (
	"strings"
	"testing"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func TestCDPDomainPremiseHasNoOrdinaryOrigin(t *testing.T) {
	vectors, err := sliccbrowsers.Protected()
	if err != nil {
		t.Fatal(err)
	}
	origins, err := protect.New(vectors.Origins)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range vectors.URLs {
		if !strings.HasSuffix(item.URL, "/cdp-premise") {
			continue
		}
		count++
		if !item.Protected || !origins.Loaded(item.URL) || !origins.TargetWith(item.URL, "page", false, protect.NoOpener, protect.Evidence{}) {
			t.Errorf("ordinary origin or target in protected premise: %s", item.URL)
		}
	}
	if count != 10 {
		t.Fatalf("protected premise has %d cases, want 10", count)
	}
}
