//go:build !windows

package fs

import (
	"path/filepath"
	"strings"
)

func relName(root, name string) (string, bool) {
	rel, err := filepath.Rel(root, name)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
