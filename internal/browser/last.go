package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

const lastFile = "last-launch.json"

type Last struct {
	ID      string `json:"id"`
	Profile string `json:"profile,omitempty"`
}

func lastPath(o Options) string {
	if !discoveryFor(o).profiles {
		return ""
	}
	o = o.withDefaults()
	return filepath.Join(filepath.Dir(o.profilesDir(o.platform(runtime.GOOS))), lastFile)
}

func LoadLast(o Options) Last {
	var l Last
	p := lastPath(o)
	if p == "" {
		return l
	}
	b, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(b, &l) != nil {
		return Last{}
	}
	return l
}

func SaveLast(o Options, l Last) error {
	p := lastPath(o)
	if p == "" || l.ID == "" {
		return nil
	}
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
