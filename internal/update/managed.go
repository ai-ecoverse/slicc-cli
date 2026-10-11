package update

import (
	"fmt"
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
	Go        Manager = "go"
)

func TakeLauncher() string {
	v := os.Getenv(LauncherEnv)
	_ = os.Unsetenv(LauncherEnv)
	return v
}

func ManagedBy(exe, launcher string, goInstalled bool) Manager {
	if goInstalled {
		return Go
	}
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

func (m Manager) Advice(exe string) string {
	switch m {
	case Go:
		return "slicc was installed with go install; update it with `" + GoInstall + "`"
	case NPM:
		return npmAdvice(exe)
	case AppBundle:
		return "slicc runs inside an app bundle; the app updates it"
	default:
		return ""
	}
}

func (m Manager) UpdateCommand() string {
	if m == Go {
		return GoInstall
	}
	return "slicc update"
}

func npmAdvice(exe string) string {
	pkg := "@ai-ecoverse/slicc@latest"
	if fromSliccy(exe) {
		pkg = "sliccy@next"
	}
	if hasSegment(exe, "_npx") {
		return fmt.Sprintf("slicc was installed with npm (npx); run `npx %s` for the latest", pkg)
	}
	return fmt.Sprintf("slicc was installed with npm; update it with `npm install -g %s`, or run the latest with `npx %s`", pkg, pkg)
}

func hasSegment(exe, name string) bool {
	for _, part := range strings.Split(filepath.ToSlash(exe), "/") {
		if part == name {
			return true
		}
	}
	return false
}

func fromSliccy(exe string) bool {
	parts := strings.Split(filepath.ToSlash(exe), "/")
	last := -1
	for i, part := range parts {
		if part != "node_modules" {
			continue
		}
		last = i
		if i+1 < len(parts) && parts[i+1] == "sliccy" {
			return true
		}
	}
	if last < 0 {
		return false
	}
	root := filepath.FromSlash(strings.Join(parts[:last+1], "/"))
	_, err := os.Stat(filepath.Join(root, "sliccy", "package.json"))
	return err == nil
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
