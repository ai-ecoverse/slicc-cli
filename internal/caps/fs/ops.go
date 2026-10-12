package fs

import (
	"errors"
	"math"
	"os"
	"syscall"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

type Statfs struct {
	Bsize  int64  `json:"bsize"`
	Blocks uint64 `json:"blocks"`
	Bfree  uint64 `json:"bfree"`
	Bavail uint64 `json:"bavail"`
}

type openFlags struct {
	write     bool
	create    bool
	exclusive bool
	truncate  bool
}

type empty struct{}

func pathOp(m *Mount, op string, a args) (any, error) {
	switch op {
	case "stat", "mkdir", "rmdir", "unlink", "readlink", "setattr":
		p, err := a.path()
		if err != nil {
			return nil, err
		}
		t, err := m.resolve(p)
		if err != nil {
			return nil, err
		}
		if op == "rmdir" || op == "unlink" {
			if err := guard(t); err != nil {
				return nil, err
			}
		}
		return onePath(op, t, a)
	case "rename":
		return rename(m, a)
	case "symlink":
		return symlink(m, a)
	case "statfs":
		return statfsOf(m.Root)
	}
	return nil, fail("EINVAL", "unknown op")
}

func onePath(op string, t target, a args) (any, error) {
	switch op {
	case "stat":
		st, err := lstatInfo(t.path)
		if err != nil {
			return nil, err
		}
		return st.attr(), nil
	case "mkdir":
		return empty{}, makeDir(t.path)
	case "rmdir":
		if t.root {
			return nil, fail("EBUSY")
		}
		return empty{}, removeDir(t.path)
	case "unlink":
		if t.root {
			return nil, fail("EBUSY")
		}
		st, err := lstatInfo(t.path)
		if err != nil {
			return nil, err
		}
		if st.kind == KindDirectory {
			return nil, fail("EISDIR")
		}
		return empty{}, removeFile(t.path, st)
	case "readlink":
		target, err := os.Readlink(t.path)
		if err != nil {
			return nil, err
		}
		return struct {
			Target string `json:"target"`
		}{target}, nil
	}
	return empty{}, setattr(t.path, a)
}

func rename(m *Mount, a args) (any, error) {
	fromRel, err := a.required("from")
	if err != nil {
		return nil, err
	}
	from, err := m.resolve(fromRel)
	if err != nil {
		return nil, err
	}
	toRel, err := a.required("to")
	if err != nil {
		return nil, err
	}
	to, err := m.resolve(toRel)
	if err != nil {
		return nil, err
	}
	if from.root || to.root {
		return nil, fail("EBUSY")
	}
	if err := guard(from); err != nil {
		return nil, err
	}
	if err := guard(to); err != nil {
		return nil, err
	}
	if err := renamePath(from.path, to.path); err != nil {
		if errors.Is(err, syscall.EEXIST) || errors.Is(err, os.ErrExist) {
			return nil, fail("ENOTEMPTY")
		}
		return nil, err
	}
	return empty{}, nil
}

func symlink(m *Mount, a args) (any, error) {
	target, ok := a.str("target")
	if !ok || containsNUL(target) {
		return nil, fail("EINVAL")
	}
	p, err := a.path()
	if err != nil {
		return nil, err
	}
	t, err := m.resolve(p)
	if err != nil {
		return nil, err
	}
	if err := guard(t); err != nil {
		return nil, err
	}
	return empty{}, makeSymlink(target, t.path)
}

func containsNUL(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}

func mtimeNs(ms float64) int64 {
	sec := math.Floor(ms / 1000)
	return int64(sec)*1e9 + int64(math.Round((ms-sec*1000)*1e6))
}

func setattr(p string, a args) error {
	mode, hasMode, err := a.mode("mode")
	if err != nil {
		return err
	}
	mtime, mtimeOK := a.number("mtime")
	if a.has("mtime") && (!mtimeOK || math.IsInf(mtime, 0) || math.Abs(mtime) > 8.64e15) {
		return fail("EINVAL")
	}
	var size int64
	if a.has("size") {
		if size, err = a.offset("size"); err != nil {
			return err
		}
	}
	st, err := lstatInfo(p)
	if err != nil {
		return err
	}
	if a.has("size") {
		if err := resize(p, st, size); err != nil {
			return err
		}
	}
	if hasMode {
		if st.kind == KindSymlink {
			return fail("EINVAL", "cannot chmod a symlink")
		}
		if err := changeMode(p, mode); err != nil {
			return err
		}
	}
	if a.has("mtime") {
		return setMtime(p, st, mtimeNs(mtime))
	}
	return nil
}

func resize(p string, st info, size int64) error {
	if st.kind == KindDirectory {
		return fail("EISDIR")
	}
	if !st.regular {
		return fail("EINVAL", "not a regular file")
	}
	f, err := openNoFollow(p, openFlags{write: true}, 0)
	if err != nil {
		return err
	}
	err = f.Truncate(size)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func openFile(m *Mount, rel string, fl openFlags, mode uint32) (*os.File, info, error) {
	t, err := m.resolve(rel)
	if err != nil {
		return nil, info{}, err
	}
	if leaf, err := lstatInfo(t.path); err == nil {
		switch leaf.kind {
		case KindSymlink:
			return nil, info{}, fail("ELOOP", "is a symlink")
		case KindDirectory:
			if fl.create && fl.exclusive {
				return nil, info{}, fail("EEXIST", systemMessages["EEXIST"])
			}
			return nil, info{}, fail("EISDIR")
		}
	}
	f, err := openNoFollow(t.path, fl, mode)
	if err != nil {
		return nil, info{}, err
	}
	st, err := fstatInfo(f)
	switch {
	case err != nil:
	case st.kind == KindDirectory:
		err = fail("EISDIR")
	case !st.regular:
		err = fail("EINVAL", "not a regular file")
	}
	if err != nil {
		_ = f.Close()
		return nil, info{}, err
	}
	return f, st, nil
}

type opened struct {
	FH   uint64 `json:"fh"`
	Attr Attr   `json:"attr"`
}

func (s *Server) open(l *link.Link, m *Mount, a args, writing bool) (any, error) {
	mode, hasMode, err := a.mode("mode")
	if err != nil {
		return nil, err
	}
	if !hasMode {
		mode = 0o666
	}
	p, err := a.path()
	if err != nil {
		return nil, err
	}
	create := a.truthy("create")
	fl := openFlags{write: writing, create: create, exclusive: create && a.truthy("exclusive"), truncate: a.truthy("truncate")}
	if writing {
		s.ns.Lock()
	} else {
		s.ns.RLock()
	}
	f, st, err := openFile(m, p, fl, mode)
	if writing {
		s.ns.Unlock()
	} else {
		s.ns.RUnlock()
	}
	if err != nil {
		return nil, err
	}
	if !writing {
		_ = f.Close()
		f = nil
	}
	ss, err := s.session(l)
	var fh uint64
	if err == nil {
		fh, err = ss.add(&handle{m: m, path: p, file: f})
	}
	if err != nil {
		if f != nil {
			_ = f.Close()
		}
		return nil, err
	}
	if writing {
		s.logf("fs open %s/%s", m.Name, p)
	}
	return opened{FH: fh, Attr: st.attr()}, nil
}

func (s *Server) release(l *link.Link, m *Mount, a args) (any, error) {
	h, err := s.handleOf(l, m, a, true)
	if err != nil {
		return nil, err
	}
	if h.file == nil {
		return empty{}, nil
	}
	defer func() { _ = h.file.Close() }()
	st, err := fstatInfo(h.file)
	if err != nil {
		return nil, err
	}
	return struct {
		Attr Attr `json:"attr"`
	}{st.attr()}, nil
}
