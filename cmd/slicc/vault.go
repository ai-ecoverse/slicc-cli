package main

import (
	"flag"
	"fmt"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/fence"
	"github.com/ai-ecoverse/slicc-cli/internal/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/ai-ecoverse/slicc-cli/internal/vault"
)

const vaultUsage = `  --no-vault           don't listen on loopback for seven's vault
                       (vault.sliccy.ai), which pairs with slicc over CDP
  --vault-origin <origin>
                       also pair with a vault on this origin (repeatable)
`

type vaultFlags struct {
	off     bool
	origins multi
}

func (v *vaultFlags) register(fs *flag.FlagSet) {
	fs.BoolVar(&v.off, "no-vault", false, "")
	fs.Var(&v.origins, "vault-origin", "")
}

func (v *vaultFlags) validate() error {
	for i, o := range v.origins {
		n := browser.NormalizeOrigin(o)
		if n == "" {
			return fmt.Errorf("--vault-origin %q is not an origin (scheme://host[:port])", o)
		}
		v.origins[i] = n
	}
	return nil
}

type vaultRun struct {
	l      *vault.Listener
	jerk   *jerk.Jerk
	pairer *vault.Pairer
}

func (v vaultFlags) start(withJerk bool, status func(string, ...any)) *vaultRun {
	if v.off {
		return nil
	}
	l, err := vault.Listen(append([]string{vault.DefaultOrigin}, v.origins...))
	if err != nil {
		status("the vault listener is off: %v", err)
		return nil
	}
	r := &vaultRun{l: l}
	if withJerk {
		r.jerk = jerk.Find(jerkDirs()...)
	}
	return r
}

func (r *vaultRun) port() uint16 {
	if r == nil {
		return 0
	}
	return uint16(r.l.Port())
}

func (r *vaultRun) serve(n *node.Node, logf func(string, ...any)) {
	if r == nil {
		return
	}
	r.l.Serve(vault.Config{Dialer: n.Dialer(), Version: version, Jerk: r.jerk, Logf: logf})
}

func (r *vaultRun) pair(client *cdp.Client, origins *signal.Origins, name string, status, logf func(string, ...any)) func(cdp.TargetInfo) {
	if r == nil || client == nil {
		return nil
	}
	dir, err := fence.ConfigDir()
	if err != nil {
		status("vault pairing is off: %v", err)
		return nil
	}
	if name == "" {
		name = "slicc on " + node.DefaultHost()
	}
	r.pairer = vault.NewPairer(vault.PairOptions{
		Client: client, Listener: r.l, KeyDir: dir, Seven: origins.Allowed,
		Node: signal.NodeID(), Name: name, Jerk: r.jerk != nil, Logf: logf, Status: status,
	})
	return r.pairer.Target
}

func (r *vaultRun) close() {
	if r == nil {
		return
	}
	if r.pairer != nil {
		r.pairer.Close()
	}
	_ = r.l.Close()
}
