//go:build !wasip1

package main

import (
	"io"
	"os"

	hostfs "github.com/ai-ecoverse/slicc-cli/internal/caps/fs"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/kernelin"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
)

func (f defaultFlags) fallbackAllowed() bool {
	return !f.noFallback && len(f.browsers) == 0 && len(f.cdp) == 0 && f.profile == "" && f.six == nil
}

func (f defaultFlags) openFlags(mounts []hostfs.Mount) openFlags {
	o := openFlags{
		page: f.page, origins: f.origins, name: f.name, allow: f.allow, deny: f.deny, expose: f.expose,
		kernelPort: f.kernel, noKernel: f.noKernel, mounts: f.mounts, noJerk: f.noJerk, compute: f.compute, loaded: mounts,
	}
	if o.noKernel || o.kernelPort == 0 {
		o.kernelPort = kernelin.DefaultPort
	}
	if o.loaded == nil {
		o.loaded = []hostfs.Mount{}
	}
	return o
}

func runFallback(f defaultFlags, mounts []hostfs.Mount, stdout, stderr io.Writer) int {
	tty := isTerminal(os.Stdin) && isTerminal(stdout)
	return serveOpen(f.openFlags(mounts), openOptions{mode: "default", open: tty || f.open, json: f.json, notice: flow.FallbackNotice}, os.Stdin, stdout, stderr)
}
