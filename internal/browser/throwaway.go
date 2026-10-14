package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var ThrowawaySwitches = []string{"--use-mock-keychain", "--password-store=basic"}

func Throwaway(profileDir string) bool {
	dir := resolvedPath(profileDir)
	if dir == "" {
		return false
	}
	root := resolvedPath(os.TempDir())
	return root != "" && within(root, dir)
}

func resolvedPath(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	rest := ""
	for cur := abs; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

func within(root, dir string) bool {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		root, dir = strings.ToLower(root), strings.ToLower(dir)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
