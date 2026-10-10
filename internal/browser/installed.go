package browser

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"sync"
)

func DiscoverInstalled(ctx context.Context, o Options) []Installed {
	if !discoveryFor(o).installed {
		return nil
	}
	o = o.withDefaults()
	p := o.platform(runtime.GOOS)
	var list []Installed
	switch runtime.GOOS {
	case "darwin":
		list = discoverMac(ctx, o, p)
	case "windows":
		list = discoverWindows(p, registryExePaths())
	default:
		status, _ := os.ReadFile("/var/lib/dpkg/status")
		list = discoverLinux(ctx, p, linuxFS{lookPath: exec.LookPath, dpkgStatus: string(status)})
	}
	return dedupeInstalled(list)
}

func dedupeInstalled(list []Installed) []Installed {
	seen := map[string]bool{}
	var out []Installed
	for _, in := range list {
		if seen[in.ID] {
			continue
		}
		seen[in.ID] = true
		out = append(out, in)
	}
	return out
}

type Result struct {
	Entries   []Entry
	Endpoints []Endpoint
	Installed []Installed
	Errors    []error
}

func Discover(ctx context.Context, o Options) Result {
	var r Result
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		r.Endpoints, r.Errors = DiscoverEndpoints(ctx, o)
	}()
	go func() {
		defer wg.Done()
		r.Installed = DiscoverInstalled(ctx, o)
	}()
	wg.Wait()
	r.Entries = Rank(r.Endpoints, r.Installed)
	return r
}
