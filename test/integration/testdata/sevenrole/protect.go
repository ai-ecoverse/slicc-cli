package protect

import "testing"

func TestSevenRolePilotSevenOrVaultHost(t *testing.T) {
	o := MustNew(nil)
	for _, c := range []struct{ name, url string }{
		{"Exact", "https://lease-pilot.sliccy.ai/"},
		{"Upper", "https://LEASE-PILOT.SLICCY.AI/"},
		{"TrailingDot", "https://lease-pilot.sliccy.ai./"},
		{"Port443", "https://lease-pilot.sliccy.ai:443/"},
		{"SubDomain", "https://a.lease-pilot.sliccy.ai/"},
		{"Port8443", "https://lease-pilot.sliccy.ai:8443/"},
		{"HTTP", "http://lease-pilot.sliccy.ai/"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if o.SevenOrVaultHost(c.url) {
				t.Errorf("SevenOrVaultHost(%q) = true, want not seven's", c.url)
			}
			if !o.Loaded(c.url) {
				t.Errorf("Loaded(%q) = false, want protected", c.url)
			}
		})
	}
}

func TestSevenRolePilotKeptToday(t *testing.T) {
	o := MustNew(nil)
	for _, c := range []struct{ name, url string }{
		{"Root", "https://lease-pilot.sliccy.ai/"},
		{"OS", "https://lease-pilot.sliccy.ai/os/x"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if o.Kept(c.url) {
				t.Errorf("Kept(%q) = true, want the pinned catalog's false", c.url)
			}
		})
	}
}

func TestSevenRoleProtectControls(t *testing.T) {
	o := MustNew(nil)
	for _, label := range []string{"seven", "www", "lease", "lease-pilot-x", "x-lease-pilot"} {
		t.Run(label, func(t *testing.T) {
			root := "https://" + label + ".sliccy.ai/"
			if !o.SevenOrVaultHost(root) {
				t.Errorf("SevenOrVaultHost(%q) = false, want seven's", root)
			}
			if os := root + "os/x"; !o.Kept(os) {
				t.Errorf("Kept(%q) = false, want kept", os)
			}
			if o.Kept(root) {
				t.Errorf("Kept(%q) = true, want only /os/ kept", root)
			}
		})
	}
	for _, label := range []string{"vault", "vault-preview", "vault-main"} {
		t.Run(label, func(t *testing.T) {
			root := "https://" + label + ".sliccy.ai/"
			if !o.Kept(root) {
				t.Errorf("Kept(%q) = false, want the vault kept", root)
			}
			if !o.SevenOrVaultHost(root) {
				t.Errorf("SevenOrVaultHost(%q) = false, want the vault", root)
			}
		})
	}
}
