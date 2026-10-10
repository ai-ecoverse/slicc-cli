package fs

import (
	"path/filepath"
	"strconv"
	"strings"
)

const (
	KindFile      = "file"
	KindDirectory = "directory"
	KindSymlink   = "symlink"
)

type info struct {
	kind    string
	regular bool
	size    int64
	mtimeNs int64
	atimeNs int64
	mode    uint32
	ino     uint64
}

type Attr struct {
	Kind  string  `json:"kind"`
	Size  int64   `json:"size"`
	Mtime float64 `json:"mtime"`
	Mode  uint32  `json:"mode"`
	Ino   uint64  `json:"ino"`
	Etag  string  `json:"etag"`
}

func (i info) etag() string {
	return `"` + strconv.FormatInt(i.size, 10) + "-" + strconv.FormatInt(i.mtimeNs, 10) + "-" + strconv.FormatUint(i.ino, 10) + `"`
}

func (i info) attr() Attr {
	return Attr{Kind: i.kind, Size: i.size, Mtime: float64(i.mtimeNs) / 1e6, Mode: i.mode & 0o7777, Ino: i.ino, Etag: i.etag()}
}

type target struct {
	path string
	root bool
}

func segments(rel string) ([]string, error) {
	if strings.ContainsAny(rel, forbidden) {
		return nil, fail("EINVAL")
	}
	if strings.HasPrefix(rel, "/") {
		return nil, fail("EACCES", "absolute path")
	}
	var parts []string
	for _, p := range strings.Split(rel, "/") {
		switch p {
		case "", ".":
		case "..":
			return nil, fail("EACCES")
		default:
			parts = append(parts, p)
		}
	}
	return parts, nil
}

func within(root, p string) bool {
	if p == root {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(p, prefix)
}

func (m *Mount) resolve(rel string) (target, error) {
	parts, err := segments(rel)
	if err != nil {
		return target{}, err
	}
	if len(parts) == 0 {
		return target{path: m.Root, root: true}, nil
	}
	parent := m.Root
	if len(parts) > 1 {
		if parent, err = realpath(filepath.Join(append([]string{m.Root}, parts[:len(parts)-1]...)...)); err != nil {
			return target{}, err
		}
		if !within(m.Root, parent) {
			return target{}, fail("EACCES")
		}
		if err := isDirectory(parent); err != nil {
			return target{}, err
		}
	}
	return target{path: filepath.Join(parent, parts[len(parts)-1])}, nil
}
