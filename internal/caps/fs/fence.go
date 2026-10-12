package fs

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/ai-ecoverse/slicc-cli/internal/fence"
)

const fencedMessage = "slicc never shares this path"

type fenceSet struct {
	keys []string
}

var fenced = sync.OnceValue(func() *fenceSet { return newFence(fence.Paths()) })

func newFence(paths []string) *fenceSet {
	f := &fenceSet{}
	seen := map[string]bool{}
	for _, p := range paths {
		for _, q := range []string{p, realOf(p)} {
			if k := fenceKey(q); k != "" && !seen[k] {
				seen[k] = true
				f.keys = append(f.keys, k)
			}
		}
	}
	return f
}

func realOf(p string) string {
	cur := filepath.Clean(p)
	var rest []string
	for {
		if r, err := realpath(cur); err == nil {
			if len(rest) == 0 {
				return r
			}
			return strings.TrimRight(r, string(filepath.Separator)) + string(filepath.Separator) + strings.Join(rest, string(filepath.Separator))
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
}

func fenceKey(p string) string {
	if p == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		if rest, ok := strings.CutPrefix(p, `\\?\UNC\`); ok {
			p = `\\` + rest
		} else {
			p = strings.TrimPrefix(p, `\\?\`)
		}
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

func (f *fenceSet) covers(p string) bool {
	k := fenceKey(p)
	for _, fk := range f.keys {
		if k == fk || strings.HasPrefix(k, fk+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (f *fenceSet) guards(p string) bool {
	k := strings.TrimRight(fenceKey(p), string(filepath.Separator)) + string(filepath.Separator)
	for _, fk := range f.keys {
		if strings.HasPrefix(fk, k) {
			return true
		}
	}
	return false
}

func Fenced(root string) bool {
	return fenced().covers(root)
}
