//go:build !wasip1

package main

import (
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/fs"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

const mountUsage = `  --mount <path>[:<name>][:ro]
                       share this folder with seven's kernel (the fs
                       capability, repeatable); the name defaults to the
                       folder's own, and :ro makes it read-only; slicc's
                       own keys, browser profiles, slicc-jerk's and fnox's
                       files are never shared
`

func serveMounts(n *node.Node, mounts []fs.Mount, status func(string, ...any)) func() {
	_, closeMounts := shareMounts(n, mounts, status)
	return closeMounts
}

func shareMounts(n *node.Node, mounts []fs.Mount, status func(string, ...any)) (*fs.Server, func()) {
	srv := fs.Register(n, mounts)
	if srv == nil {
		return nil, func() {}
	}
	names := make([]string, 0, len(mounts))
	for _, m := range mounts {
		if m.ReadOnly {
			names = append(names, m.Name+" (read-only)")
		} else {
			names = append(names, m.Name)
		}
	}
	status("fs: sharing %s", strings.Join(names, ", "))
	return srv, func() { _ = srv.Close() }
}
