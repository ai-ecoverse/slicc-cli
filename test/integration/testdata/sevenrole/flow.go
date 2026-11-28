package flow

import (
	"slices"
	"testing"
)

func TestSevenRolePageFlag(t *testing.T) {
	for _, c := range []struct {
		name, page, refused string
		want                []string
	}{
		{"Pilot", "https://lease-pilot.sliccy.ai/", "https://lease-pilot.sliccy.ai", nil},
		{"PilotUpper", "https://LEASE-PILOT.SLICCY.AI/", "https://lease-pilot.sliccy.ai", nil},
		{"Pilot443", "https://lease-pilot.sliccy.ai:443/", "https://lease-pilot.sliccy.ai", nil},
		{"Seven", "https://seven.sliccy.ai/", "", nil},
		{"Default", DefaultPage, "", nil},
		{"Six", SixPage, "", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := PageOrigins(c.page, nil)
			if c.refused != "" {
				if want := "--page " + c.refused + " is on a protected host and is not a seven page"; err == nil || err.Error() != want || got != nil {
					t.Errorf("PageOrigins(%q) = %q, %v; want the refusal %q", c.page, got, err, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("PageOrigins(%q): %v", c.page, err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("PageOrigins(%q) = %q, want %q", c.page, got, c.want)
			}
		})
	}
}
