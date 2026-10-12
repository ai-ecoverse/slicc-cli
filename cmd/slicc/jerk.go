package main

import (
	"path/filepath"

	capjerk "github.com/ai-ecoverse/slicc-cli/internal/caps/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/fence"
	"github.com/ai-ecoverse/slicc-cli/internal/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

const jerkLocalUsage = `  --no-jerk            don't offer this machine's slicc-jerk to seven (the
                       jerk and jerk-seal capabilities)
`

const jerkFollowUsage = `  --jerk               offer this machine's slicc-jerk to seven for status
                       and sign-ins (the jerk capability)
  --jerk-seal          also let seven seal this machine's tokens into its
                       vault (jerk-seal; implies --jerk)
`

func jerkDirs() []string {
	if d, err := fence.DataDir(); err == nil {
		return []string{filepath.Join(d, "bin")}
	}
	return nil
}

func serveJerk(n *node.Node, seal, explicit bool, status func(string, ...any)) {
	j := jerk.Find(jerkDirs()...)
	if j == nil {
		if explicit {
			status("jerk: no slicc-jerk %s or later on PATH or in slicc's data folder; not offering it", jerk.MinVersion)
		}
		return
	}
	capjerk.Register(n, capjerk.Options{Jerk: j, Seal: seal})
	caps := "jerk"
	if seal {
		caps = "jerk, jerk-seal"
	}
	status("jerk: offering slicc-jerk %s (%s)", j.Version, caps)
}
