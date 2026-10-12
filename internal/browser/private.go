package browser

import "runtime"

func PrivateDirs(o Options) []string {
	o = o.withDefaults()
	p := o.platform(runtime.GOOS)
	dirs := []string{o.profilesDir(p), p.profilesDir(), p.legacyProfilesDir(), p.sixRoot()}
	if d := p.sixEnvDir(); d != "" {
		dirs = append(dirs, d)
	}
	for _, k := range knownBrowsers {
		dirs = append(dirs, p.userDataDirs(k)...)
		if p.goos == "linux" && k.linux.snap != "" {
			dirs = append(dirs, p.snapProfilesDir(k.linux.snap), p.snapLegacyProfilesDir(k.linux.snap))
		}
	}
	return uniqueStrings(dirs)
}
