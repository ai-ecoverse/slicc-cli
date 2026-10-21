package link

import (
	"encoding/json"
	"fmt"
)

const (
	Version     = 1
	ControlName = "slicc.link.v1"
	StreamName  = "slicc.link.v1.stream"
)

type Role string

const (
	RolePage Role = "page"
	RoleNode Role = "node"
)

func (r Role) peer() Role {
	if r == RolePage {
		return RoleNode
	}
	return RolePage
}

type Routes struct {
	Prefixes []string `json:"prefixes"`
	Exit     bool     `json:"exit"`
}

type Policy struct {
	Allow   []string `json:"allow"`
	Deny    []string `json:"deny"`
	Summary string   `json:"summary"`
}

type Browser struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Seven   bool   `json:"seven"`
}

type FSCapabilities struct {
	MaxIO           int    `json:"maxIo"`
	Symlinks        bool   `json:"symlinks"`
	Chmod           bool   `json:"chmod"`
	CaseInsensitive bool   `json:"caseInsensitive"`
	Normalization   string `json:"normalization"`
	Ranges          bool   `json:"ranges"`
}

type Mount struct {
	Name         string         `json:"name"`
	Readonly     bool           `json:"readonly"`
	Capabilities FSCapabilities `json:"capabilities"`
}

type Hello struct {
	V          int        `json:"v"`
	Role       Role       `json:"role"`
	Name       string     `json:"name,omitempty"`
	Host       string     `json:"host,omitempty"`
	Version    string     `json:"version,omitempty"`
	Mode       string     `json:"mode,omitempty"`
	Node       string     `json:"node,omitempty"`
	Caps       []string   `json:"caps"`
	Routes     *Routes    `json:"routes,omitempty"`
	Policy     *Policy    `json:"policy,omitempty"`
	SSHHostKey string     `json:"sshHostKey,omitempty"`
	Browsers   []Browser  `json:"browsers,omitempty"`
	Mounts     []Mount    `json:"mounts,omitempty"`
	Jerk       *Jerk      `json:"jerk,omitempty"`
	Clipboard  *Clipboard `json:"clipboard,omitempty"`
	Kernel     *Kernel    `json:"kernel,omitempty"`

	Raw json.RawMessage `json:"-"`
}

type Jerk struct {
	Version string `json:"version"`
}

type Clipboard struct {
	Backend string `json:"backend"`
}

type Kernel struct {
	Port int `json:"port"`
}

func (h Hello) HasCap(c string) bool {
	for _, x := range h.Caps {
		if x == c {
			return true
		}
	}
	return false
}

func parseHello(data []byte, want Role) (Hello, error) {
	var h Hello
	if err := json.Unmarshal(data, &h); err != nil {
		return Hello{}, fmt.Errorf("bad HELLO: %w", err)
	}
	if h.V < 1 {
		return Hello{}, fmt.Errorf("bad HELLO: version %d", h.V)
	}
	if h.Role != want {
		return Hello{}, fmt.Errorf("bad HELLO: role %q, want %q", h.Role, want)
	}
	h.Raw = append(json.RawMessage(nil), data...)
	return h, nil
}
