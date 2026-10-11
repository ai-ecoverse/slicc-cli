package update

import "runtime/debug"

const (
	ModulePath = "github.com/ai-ecoverse/slicc-cli"
	GoInstall  = "go install " + ModulePath + "/cmd/slicc@latest"
)

type Build struct {
	Version string
	Go      bool
}

func Identify(linked string) Build {
	info, _ := debug.ReadBuildInfo()
	return fromBuildInfo(linked, info)
}

func fromBuildInfo(linked string, info *debug.BuildInfo) Build {
	if linked != "dev" || info == nil || info.Main.Path != ModulePath || info.Main.Sum == "" {
		return Build{Version: linked}
	}
	v := info.Main.Version
	if v == "" || v == "(devel)" {
		return Build{Version: linked}
	}
	return Build{Version: Normalize(v), Go: true}
}
