//go:build !wasip1

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	hostfs "github.com/ai-ecoverse/slicc-cli/internal/caps/fs"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
	"github.com/ai-ecoverse/slicc-cli/internal/tui"
)

type liveCaps struct {
	mu      sync.Mutex
	given   computeFlags
	compute computeFlags
	mounts  []hostfs.Mount
	jerk    bool
	clip    tui.Clip
	kernel  int
	fs      *hostfs.Server
	relink  chan flow.Relink
}

type capsSnapshot struct {
	compute computeFlags
	mounts  []hostfs.Mount
	jerk    bool
	clip    tui.Clip
	kernel  int
}

func newLiveCaps(cf computeFlags, mounts []hostfs.Mount, jerk bool, clip tui.Clip, kernel int) *liveCaps {
	return &liveCaps{given: cf, compute: cf, mounts: mounts, jerk: jerk, clip: clip, kernel: kernel, relink: make(chan flow.Relink, 1)}
}

func (lc *liveCaps) snapshot() capsSnapshot {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return capsSnapshot{compute: lc.compute, mounts: slices.Clone(lc.mounts), jerk: lc.jerk, clip: lc.clip, kernel: lc.kernel}
}

func (lc *liveCaps) setFS(old, srv *hostfs.Server) {
	lc.mu.Lock()
	if lc.fs == old {
		lc.fs = srv
	}
	lc.mu.Unlock()
}

func (lc *liveCaps) liveMounts() bool {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.fs.Live()
}

func (lc *liveCaps) settings(jerk, clip bool) tui.Settings {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	s := tui.Settings{Jerk: lc.jerk && jerk, Shell: slices.Clone(lc.compute.runner), Kernel: lc.kernel}
	if clip {
		s.Clipboard = lc.clip
	}
	for _, m := range lc.mounts {
		s.Folders = append(s.Folders, tui.Folder{Path: m.Root, Name: m.Name, ReadOnly: m.ReadOnly})
	}
	return s
}

func (lc *liveCaps) apply(s tui.Settings, relink bool, status func(string, ...any)) {
	mounts := loadFolders(s.Folders, status)
	lc.mu.Lock()
	if !relink && lc.fs != nil {
		updateMounts(lc.fs, lc.mounts, mounts, status)
		lc.mounts = mounts
		lc.mu.Unlock()
		status("fs: sharing %s", mountNames(mounts))
		return
	}
	lc.jerk, lc.clip, lc.kernel, lc.mounts = s.Jerk, s.Clipboard, s.Kernel, mounts
	lc.compute = computeFor(lc.given, s.Shell)
	rl := flow.Relink{Kernel: lc.kernel, Compute: lc.compute.dashboard()}
	lc.mu.Unlock()
	select {
	case <-lc.relink:
	default:
	}
	lc.relink <- rl
}

func computeFor(given computeFlags, runner []string) computeFlags {
	if len(runner) == 0 {
		return computeFlags{}
	}
	if slices.Equal(runner, given.runner) {
		return given
	}
	cf := computeFlags{runner: slices.Clone(runner)}
	if path, err := exec.LookPath(cf.runner[0]); err == nil {
		if abs, err := filepath.Abs(path); err == nil {
			cf.runner[0] = abs
		}
	}
	return cf
}

func loadFolders(folders []tui.Folder, status func(string, ...any)) []hostfs.Mount {
	var specs []string
	for _, f := range folders {
		if f.Off {
			continue
		}
		spec := f.Path + ":" + f.Name
		if f.ReadOnly {
			spec += ":ro"
		}
		specs = append(specs, spec)
	}
	return hostfs.Load(specs, func(format string, a ...any) { status("fs: "+format, a...) })
}

func sameMount(a, b hostfs.Mount) bool {
	return a.Name == b.Name && a.Root == b.Root && a.ReadOnly == b.ReadOnly
}

func updateMounts(srv *hostfs.Server, from, to []hostfs.Mount, status func(string, ...any)) {
	for _, m := range from {
		if !slices.ContainsFunc(to, func(n hostfs.Mount) bool { return sameMount(m, n) }) {
			srv.Remove(m.Name)
		}
	}
	for _, m := range to {
		if slices.ContainsFunc(from, func(o hostfs.Mount) bool { return sameMount(m, o) }) {
			continue
		}
		if err := srv.Add(m); err != nil {
			status("fs: %s: %v", m.Name, err)
		}
	}
}

func mountNames(mounts []hostfs.Mount) string {
	if len(mounts) == 0 {
		return "no folders"
	}
	names := make([]string, 0, len(mounts))
	for _, m := range mounts {
		if m.ReadOnly {
			names = append(names, m.Name+" (read-only)")
		} else {
			names = append(names, m.Name)
		}
	}
	return strings.Join(names, ", ")
}

func defaultRunner() []string {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("pwsh"); err == nil {
			return []string{"pwsh", "-Command"}
		}
		return []string{"powershell", "-Command"}
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return []string{sh, "-c"}
	}
	return []string{"/bin/sh", "-c"}
}

func statusTo(send func(flow.Event)) func(string, ...any) {
	return func(format string, a ...any) {
		send(flow.Event{Event: flow.Status, Message: fmt.Sprintf(format, a...)})
	}
}
