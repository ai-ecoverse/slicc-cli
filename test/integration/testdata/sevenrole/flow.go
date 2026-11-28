package flow

import (
	"slices"
	"testing"
)

func TestSevenRolePageFlag(t *testing.T) {
	for _, c := range []struct {
		name, page string
		want       []string
	}{
		{"Pilot", "https://lease-pilot.sliccy.ai/", []string{"https://lease-pilot.sliccy.ai"}},
		{"PilotUpper", "https://LEASE-PILOT.SLICCY.AI/", []string{"https://lease-pilot.sliccy.ai"}},
		{"Pilot443", "https://lease-pilot.sliccy.ai:443/", []string{"https://lease-pilot.sliccy.ai"}},
		{"Seven", "https://seven.sliccy.ai/", nil},
		{"Default", DefaultPage, nil},
		{"Six", SixPage, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := PageOrigins(c.page, nil)
			if err != nil {
				t.Fatalf("PageOrigins(%q): %v", c.page, err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("PageOrigins(%q) = %q, want %q", c.page, got, c.want)
			}
		})
	}
}
