package signal

import (
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

func TestSevenRoleAttachCandidates(t *testing.T) {
	o, err := NewOrigins(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &CDP{opts: CDPOptions{Origins: o, Tab: func(string) bool { return true }}}
	pilot := cdp.TargetInfo{TargetID: "PILOT", Type: "page", URL: "https://lease-pilot.sliccy.ai/"}
	seven := cdp.TargetInfo{TargetID: "SEVEN", Type: "page", URL: "https://seven.sliccy.ai/"}
	t.Run("PilotAndSeven", func(t *testing.T) {
		got := c.candidates([]cdp.TargetInfo{pilot, seven})
		if len(got) != 1 || got[0].TargetID != "SEVEN" {
			t.Errorf("candidates = %+v, want only SEVEN", got)
		}
	})
	t.Run("PilotOnly", func(t *testing.T) {
		if got := c.candidates([]cdp.TargetInfo{pilot}); len(got) != 0 {
			t.Errorf("candidates = %+v, want none", got)
		}
	})
	t.Run("PilotOrigin", func(t *testing.T) {
		if o.Allowed("https://lease-pilot.sliccy.ai") {
			t.Error("Allowed(pilot origin) = true, want refused")
		}
		if o.AllowedURL("https://lease-pilot.sliccy.ai:443/os/") {
			t.Error("AllowedURL(pilot :443 URL) = true, want refused")
		}
	})
	t.Run("SevenOrigin", func(t *testing.T) {
		if !o.Allowed("https://seven.sliccy.ai") || !o.AllowedURL("https://www.sliccy.ai/os/") {
			t.Error("seven's origins refused")
		}
	})
}
