package browser

import "os"

const DiscoveryEnv = "SLICC_DISCOVERY"

type discovery struct {
	probe     bool
	userData  bool
	profiles  bool
	installed bool
	process   bool
}

func discoveryFor(o Options) discovery {
	mode := os.Getenv(DiscoveryEnv)
	if mode == "" {
		return discovery{probe: true, userData: true, profiles: true, installed: true, process: true}
	}
	d := discovery{
		probe:    o.ProbePorts != nil,
		userData: o.Home != "",
		profiles: o.Home != "" || o.ProfilesDir != "",
		process:  o.Processes != nil,
	}
	switch mode {
	case "home":
		d.userData, d.profiles, d.installed = true, true, true
	case "installed":
		d.installed = true
	}
	return d
}
