package fs

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

const (
	MaxIO      = 16 * 1024 * 1024
	MaxHandles = 4096
)

type Mount struct {
	Name         string
	Root         string
	ReadOnly     bool
	Capabilities link.FSCapabilities
}

var validName = regexp.MustCompile(`^[^/\\\x00]+$`)

func ParseSpec(spec string) (path, name string, readonly bool) {
	rest := spec
	if strings.HasSuffix(rest, ":ro") {
		readonly = true
		rest = strings.TrimSuffix(rest, ":ro")
	}
	if at := strings.LastIndex(rest, ":"); at > 0 {
		tail := rest[at+1:]
		if tail != "" && !strings.ContainsAny(tail, `\/`) {
			name = tail
			rest = rest[:at]
		}
	}
	return rest, name, readonly
}

func Load(specs []string, warn func(format string, args ...any)) []Mount {
	if warn == nil {
		warn = func(string, ...any) {}
	}
	var mounts []Mount
	for _, spec := range specs {
		path, name, readonly := ParseSpec(spec)
		root, err := rootOf(path)
		if err != nil {
			warn("--mount %s: not an existing folder, skipping", spec)
			continue
		}
		if name == "" {
			name = filepath.Base(root)
		}
		if !validName.MatchString(name) || name == "." || name == ".." {
			warn("--mount %s: invalid name, skipping", spec)
			continue
		}
		if taken(mounts, name) {
			warn("--mount %s: the name %s is taken, skipping", spec, name)
			continue
		}
		mounts = append(mounts, Mount{
			Name:     name,
			Root:     root,
			ReadOnly: readonly,
			Capabilities: link.FSCapabilities{
				MaxIO:           MaxIO,
				Symlinks:        symlinksSupported,
				Chmod:           chmodSupported,
				CaseInsensitive: probeCase(root),
				Normalization:   normalization(),
				Ranges:          true,
			},
		})
	}
	return mounts
}

func taken(mounts []Mount, name string) bool {
	for _, m := range mounts {
		if m.Name == name {
			return true
		}
	}
	return false
}

func rootOf(path string) (string, error) {
	if path == "" {
		return "", os.ErrNotExist
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root, err := realpath(abs)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", os.ErrInvalid
	}
	return root, nil
}

func swapCase(s string) string {
	var b strings.Builder
	for _, r := range s {
		if l := unicode.ToLower(r); l == r {
			b.WriteRune(unicode.ToUpper(r))
		} else {
			b.WriteRune(l)
		}
	}
	return b.String()
}

func probeCase(root string) bool {
	vol := filepath.VolumeName(root)
	sep := string(filepath.Separator)
	parts := strings.Split(root[len(vol):], sep)
	for i := len(parts) - 1; i > 0; i-- {
		swapped := swapCase(parts[i])
		if swapped == parts[i] {
			continue
		}
		other := vol + strings.Join(append(append(append([]string{}, parts[:i]...), swapped), parts[i+1:]...), sep)
		a, err := os.Lstat(root)
		if err != nil {
			return false
		}
		b, err := os.Lstat(other)
		if err != nil {
			return false
		}
		return os.SameFile(a, b)
	}
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
}
