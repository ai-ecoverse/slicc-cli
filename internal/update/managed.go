package update

import (
	"os"
	"path/filepath"
	"strings"
)

const LauncherEnv = "SLICC_LAUNCHER"

type Manager string

const (
	Unmanaged Manager = ""
	NPM       Manager = "npm"
	AppBundle Manager = "app"
)

func TakeLauncher() string {
	v := os.Getenv(LauncherEnv)
	_ = os.Unsetenv(LauncherEnv)
	return v
}

func ManagedBy(exe, launcher string) Manager {
	if launcher == "npm" {
		return NPM
	}
	parts := strings.Split(filepath.ToSlash(exe), "/")
	for i, part := range parts {
		if part == "node_modules" {
			return NPM
		}
		if strings.HasSuffix(part, ".app") && i+1 < len(parts) && parts[i+1] == "Contents" {
			return AppBundle
		}
	}
	return Unmanaged
}

func (m Manager) Advice() string {
	switch m {
	case NPM:
		return "slicc was installed with npm; update it with npm (npm install -g @ai-ecoverse/slicc@latest)"
	case AppBundle:
		return "slicc runs inside an app bundle; the app updates it"
	default:
		return ""
	}
}

func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}
