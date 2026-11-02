package screen

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/flock"
)

type auditFile interface {
	io.Writer
	io.Seeker
	Stat() (fs.FileInfo, error)
	Sync() error
	Close() error
}
type auditIO struct {
	mkdir   func(string, fs.FileMode) error
	lstat   func(string) (fs.FileInfo, error)
	stat    func(string) (fs.FileInfo, error)
	open    func(string, int, fs.FileMode) (auditFile, error)
	readDir func(string) ([]fs.DirEntry, error)
	remove  func(string) error
}

func diskAuditIO() auditIO {
	return auditIO{os.MkdirAll, os.Lstat, os.Stat, func(p string, f int, m fs.FileMode) (auditFile, error) { return os.OpenFile(p, f, m) }, os.ReadDir, os.Remove}
}

type Journal struct {
	io        auditIO
	mu        sync.Mutex
	dir       string
	companion string
	file      string
	day       string
}

func NewJournal(dir, companion string) (*Journal, error) {
	return newJournal(dir, companion, diskAuditIO())
}
func newJournal(dir, companion string, ops auditIO) (*Journal, error) {
	for _, p := range []string{dir, companion} {
		if p == "" {
			return nil, ErrAccess
		}
		if e := ops.mkdir(p, 0700); e != nil {
			return nil, ErrAccess
		}
		s, e := ops.lstat(p)
		if e != nil || !s.IsDir() || s.Mode()&os.ModeSymlink != 0 || s.Mode().Perm() != 0700 {
			return nil, ErrAccess
		}
	}
	day := time.Now().UTC().Format("20060102")
	return &Journal{io: ops, dir: dir, companion: companion, day: day, file: "broker-" + day + "-" + ID(16) + ".jsonl"}, nil
}
func (j *Journal) Record(event, reason, scope, session string) error {
	if (event != "grant" && event != "grant-intent" && event != "stop" && event != "refusal") || !validReason(reason) || (scope != "" && scope != "window" && scope != "display") || (session != "" && !validID(session, 16)) {
		return ErrProtocol
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	day := time.Now().UTC().Format("20060102")
	if day != j.day {
		j.day = day
		j.file = "broker-" + day + "-" + ID(16) + ".jsonl"
	}
	lockpath := filepath.Join(j.companion, "quota.lock")
	lock, e := privateFile(lockpath)
	if e != nil {
		return e
	}
	defer func() { _ = lock.Close() }()
	ok, e := flock.TryLock(lock)
	if e != nil || !ok {
		return ErrAccess
	}
	defer flock.Unlock(lock)
	row := struct {
		Minute  string `json:"minute"`
		Event   string `json:"event"`
		Reason  string `json:"reason"`
		Scope   string `json:"scope,omitempty"`
		Session string `json:"session,omitempty"`
	}{time.Now().UTC().Truncate(time.Minute).Format(time.RFC3339), event, reason, scope, session}
	data, _ := json.Marshal(row)
	data = append(data, '\n')
	if e = j.rotate(int64(len(data))); e != nil {
		return e
	}
	f, e := privateFileUsing(filepath.Join(j.dir, j.file), j.io)
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	if _, e = f.Seek(0, 2); e != nil {
		return ErrAccess
	}
	if e = writeFull(f, data); e != nil {
		return ErrAccess
	}
	if e = f.Sync(); e != nil {
		return ErrAccess
	}
	return nil
}
func privateFile(path string) (*os.File, error) {
	f, e := privateFileUsing(path, diskAuditIO())
	if e != nil {
		return nil, e
	}
	return f.(*os.File), nil
}
func privateFileUsing(path string, ops auditIO) (auditFile, error) {
	before, e := ops.lstat(path)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, ErrAccess
	}
	if e == nil && (!before.Mode().IsRegular() || before.Mode().Perm() != 0600) {
		return nil, ErrAccess
	}
	f, e := ops.open(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, ErrAccess
	}
	after, e := f.Stat()
	disk, de := ops.lstat(path)
	if e != nil || de != nil || !after.Mode().IsRegular() || !os.SameFile(after, disk) || (before != nil && !os.SameFile(before, after)) {
		_ = f.Close()
		return nil, ErrAccess
	}
	return f, nil
}
func (j *Journal) rotate(extra int64) error {
	total := int64(0)
	var files []journalFile
	for _, dir := range []string{j.dir, j.companion} {
		list, e := journalFilesUsing(dir, j.io)
		if e != nil {
			return e
		}
		for _, f := range list {
			files = append(files, f)
			total += f.size
		}
	}
	sort.Slice(files, func(i, k int) bool { return files[i].at.Before(files[k].at) })
	for _, f := range files {
		if total+extra <= 100<<20 {
			break
		}
		if j.io.remove(f.path) != nil {
			return ErrAccess
		}
		total -= f.size
	}
	if total+extra > 100<<20 {
		return ErrAccess
	}

	path := filepath.Join(j.dir, j.file)
	if s, e := j.io.stat(path); e == nil && s.Size()+extra > 10<<20 {
		j.file = fmt.Sprintf("broker-%s-%s.jsonl", j.day, ID(16))
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return ErrAccess
	}
	return nil
}
func DefaultJournal() (*Journal, error) {
	base, e := os.UserConfigDir()
	if e != nil {
		return nil, ErrAccess
	}
	return NewJournal(filepath.Join(base, "slicc", "audit"), filepath.Join(base, "ai.sliccy.mac", "Audit"))
}

type journalFile struct {
	path string
	size int64
	at   time.Time
}

func journalFilesUsing(dir string, ops auditIO) ([]journalFile, error) {
	var files []journalFile
	entries, e := ops.readDir(dir)
	if e != nil || len(entries) > 1024 {
		return nil, ErrAccess
	}
	for _, entry := range entries {
		if entry.Name() == "quota.lock" {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, ErrAccess
		}
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() {
			return nil, ErrAccess
		}
		path := filepath.Join(dir, entry.Name())
		age, known := auditAge(entry.Name(), info.ModTime())
		if !known {
			if ops.remove(path) != nil {
				return nil, ErrAccess
			}
			continue
		}
		if time.Since(age) >= 30*24*time.Hour {
			if ops.remove(path) != nil {
				return nil, ErrAccess
			}
			continue
		}
		files = append(files, journalFile{path, info.Size(), age})
	}
	return files, nil
}

func auditAge(name string, mtime time.Time) (time.Time, bool) {
	prefix := ""
	for _, p := range []string{"app-", "broker-"} {
		if strings.HasPrefix(name, p) {
			prefix = p
			break
		}
	}
	if prefix == "" || !strings.HasSuffix(name, ".jsonl") {
		return time.Time{}, false
	}
	rest := name[len(prefix):]
	if len(rest) < 10 || rest[8] != '-' {
		return time.Time{}, false
	}
	day, e := time.Parse("20060102", rest[:8])
	if e != nil {
		return time.Time{}, false
	}
	if mtime.Before(day) {
		return mtime, true
	}
	return day, true
}
