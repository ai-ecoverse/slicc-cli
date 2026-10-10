package fs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

const (
	chunk     = link.MaxPayload
	listBatch = 256
)

type readHead struct {
	Etag   string `json:"etag"`
	Size   int64  `json:"size"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
}

func (s *Server) pinned(m *Mount, h *handle) (*os.File, bool, error) {
	if h.file != nil {
		return h.file, false, nil
	}
	s.ns.RLock()
	defer s.ns.RUnlock()
	f, _, err := openFile(m, h.path, openFlags{}, 0)
	return f, true, err
}

func (s *Server) read(st *link.Stream, m *Mount, a args) error {
	offset, err := a.offset("offset")
	if err != nil {
		return err
	}
	size, err := a.offset("size")
	if err != nil || size > MaxIO {
		return fail("EINVAL")
	}
	h, err := s.handleOf(st.Link(), m, a, false)
	if err != nil {
		return err
	}
	f, owned, err := s.pinned(m, h)
	if err != nil {
		return err
	}
	if owned {
		defer func() { _ = f.Close() }()
	}
	in, err := fstatInfo(f)
	if err != nil {
		return err
	}
	etag := in.etag()
	if h.file == nil && a.has("ifMatch") {
		if v, ok := a.str("ifMatch"); !ok || v != etag {
			return fail("ESTALE")
		}
	}
	end := min(offset+size, in.size)
	length := max(0, end-offset)
	if err := st.Accept(readHead{Etag: etag, Size: in.size, Offset: offset, Length: length}); err != nil {
		return err
	}
	buf := make([]byte, min(int64(chunk), max(length, 1)))
	for pos := offset; pos < end; {
		n, rerr := f.ReadAt(buf[:min(int64(len(buf)), end-pos)], pos)
		if n > 0 {
			if _, err := st.Write(buf[:n]); err != nil {
				return err
			}
			pos += int64(n)
		}
		if errors.Is(rerr, io.EOF) && pos < end {
			return fail("ESTALE")
		}
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return rerr
		}
	}
	return st.CloseWrite()
}

type pwriter struct {
	f     *os.File
	pos   int64
	total int64
}

func (w *pwriter) Write(p []byte) (int, error) {
	if w.total+int64(len(p)) > MaxIO {
		return 0, fail("EINVAL")
	}
	n, err := w.f.WriteAt(p, w.pos)
	w.pos += int64(n)
	w.total += int64(n)
	return n, err
}

func (s *Server) write(st *link.Stream, m *Mount, a args, readonly bool) error {
	if readonly {
		return fail("EROFS")
	}
	offset, err := a.offset("offset")
	if err != nil {
		return err
	}
	h, err := s.handleOf(st.Link(), m, a, false)
	if err != nil {
		return err
	}
	if h.file == nil {
		return fail("EBADF")
	}
	if err := st.Accept(nil); err != nil {
		return err
	}
	if _, err := st.WriteTo(&pwriter{f: h.file, pos: offset}); err != nil {
		return err
	}
	return st.CloseWrite()
}

type entry struct {
	Name string `json:"name"`
	Attr Attr   `json:"attr"`
}

func listBatchOf(d *os.File, dir string) ([]byte, bool, error) {
	names, err := d.Readdirnames(listBatch)
	done := errors.Is(err, io.EOF) || len(names) == 0
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, true, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, name := range names {
		in, err := lstatInfo(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		_ = enc.Encode(entry{Name: name, Attr: in.attr()})
	}
	return buf.Bytes(), done, nil
}

func (s *Server) openListing(m *Mount, a args) (*os.File, string, []byte, bool, error) {
	p, err := a.path()
	if err != nil {
		return nil, "", nil, true, err
	}
	s.ns.RLock()
	defer s.ns.RUnlock()
	t, err := m.resolve(p)
	if err != nil {
		return nil, "", nil, true, err
	}
	own, err := lstatInfo(t.path)
	if err != nil {
		return nil, "", nil, true, err
	}
	if own.kind != KindDirectory {
		return nil, "", nil, true, fail("ENOTDIR")
	}
	d, err := openDir(t.path)
	if err != nil {
		return nil, "", nil, true, err
	}
	lines, done, err := listBatchOf(d, t.path)
	if err != nil {
		_ = d.Close()
		return nil, "", nil, true, err
	}
	return d, t.path, lines, done, nil
}

func (s *Server) nextListing(m *Mount, d *os.File, dir string) ([]byte, bool, error) {
	s.ns.RLock()
	defer s.ns.RUnlock()
	rp, err := realpath(dir)
	if err != nil || !within(m.Root, rp) {
		return nil, true, fail("ESTALE", "the directory moved during the list")
	}
	a, err1 := d.Stat()
	b, err2 := os.Stat(rp)
	if err1 != nil || err2 != nil || !os.SameFile(a, b) {
		return nil, true, fail("ESTALE", "the directory moved during the list")
	}
	return listBatchOf(d, rp)
}

func (s *Server) list(st *link.Stream, m *Mount, a args) error {
	d, dir, lines, done, err := s.openListing(m, a)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if err := st.Accept(nil); err != nil {
		return err
	}
	for {
		if len(lines) > 0 {
			if _, err := st.Write(lines); err != nil {
				return err
			}
		}
		if done {
			return st.CloseWrite()
		}
		if lines, done, err = s.nextListing(m, d, dir); err != nil {
			return err
		}
	}
}
