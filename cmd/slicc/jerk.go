//go:build !wasip1

package main

import (
	"context"
	"path/filepath"
	"time"

	capjerk "github.com/ai-ecoverse/slicc-cli/internal/caps/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
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

const unsealWithin = 75 * time.Second

func serveJerk(n *node.Node, seal, explicit bool, status func(string, ...any)) *capjerk.Server {
	j, old := jerk.Find(jerkDirs()...)
	if j == nil {
		switch {
		case old != "":
			status("jerk: host jerk %s is older than %s; run slicc install jerk", old, jerk.MinVersion)
		case explicit:
			status("jerk: no slicc-jerk %s or later on PATH or in slicc's data folder; not offering it", jerk.MinVersion)
		}
		return nil
	}
	s := capjerk.Register(n, capjerk.Options{Jerk: j, Seal: seal})
	caps := "jerk"
	if seal {
		caps = "jerk, jerk-seal"
	}
	status("jerk: offering slicc-jerk %s (%s)", j.Version, caps)
	if !seal {
		return nil
	}
	return s
}

func localJerk(off bool, n *node.Node, status func(string, ...any)) *capjerk.Server {
	if off {
		return nil
	}
	return serveJerk(n, true, false, status)
}

func serveLeases(h *ctl.Holder, s *capjerk.Server) {
	if h == nil || s == nil {
		return
	}
	h.Answer(ctl.OpLeases, func(ctl.Request) map[string]any {
		return map[string]any{"sealing": s.Sealing(), "leases": s.Leases()}
	})
	h.AnswerWithin(ctl.OpUnseal, unsealWithin, func(req ctl.Request) map[string]any {
		r := s.Unseal(context.Background(), req.LeaseID)
		switch r.Code {
		case "":
		case "EBUSY":
			return map[string]any{"ok": false, "error": r.Code}
		case "EIO":
			return map[string]any{"ok": false, "error": r.Code, "exit": r.Exit}
		default:
			return map[string]any{"error": r.Code}
		}
		out := map[string]any{"ok": true, "revoked": true}
		if r.Upstream != nil {
			out["upstream"] = *r.Upstream
		}
		return out
	})
	for op, on := range map[string]bool{ctl.OpSealOff: false, ctl.OpSealOn: true} {
		h.Answer(op, func(ctl.Request) map[string]any {
			s.SetSealing(on)
			return map[string]any{"ok": true, "sealing": on}
		})
	}
}
