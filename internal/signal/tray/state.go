package tray

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	stateFileName   = "tray-join.json"
	stateDirName    = "slicc"
	stateMaxEntries = 16
	stateVersion    = 1
)

type JoinState struct {
	path string
	mu   sync.Mutex
}

type stateFile struct {
	Version int          `json:"version"`
	Trays   []stateEntry `json:"trays"`
}

type stateEntry struct {
	Origin    string `json:"origin"`
	JoinURL   string `json:"joinUrl"`
	UpdatedAt string `json:"updatedAt"`
}

func DefaultStatePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, stateDirName, stateFileName), nil
}

func OpenDefaultState() (*JoinState, error) {
	path, err := DefaultStatePath()
	if err != nil {
		return nil, err
	}
	return OpenState(path), nil
}

func OpenState(path string) *JoinState {
	return &JoinState{path: path}
}

func (s *JoinState) Path() string { return s.path }

func originKey(joinURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(joinURL)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (s *JoinState) Current(origin string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := originKey(origin)
	for _, e := range s.load().Trays {
		if e.Origin == key && IsJoinURL(e.JoinURL) {
			return e.JoinURL
		}
	}
	return strings.TrimSpace(origin)
}

func (s *JoinState) Last() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.load().Trays {
		if IsJoinURL(e.JoinURL) {
			return e.JoinURL, true
		}
	}
	return "", false
}

func (s *JoinState) Save(origin, current string) error {
	if !IsJoinURL(current) {
		return ErrInvalidJoinURL
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := originKey(origin)
	f := s.load()
	trays := []stateEntry{{Origin: key, JoinURL: strings.TrimSpace(current), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}}
	for _, e := range f.Trays {
		if e.Origin != key && len(trays) < stateMaxEntries {
			trays = append(trays, e)
		}
	}
	return writePrivateJSON(s.path, stateFile{Version: stateVersion, Trays: trays})
}

func (s *JoinState) Forget(origin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := originKey(origin)
	f := s.load()
	trays := f.Trays[:0]
	for _, e := range f.Trays {
		if e.Origin != key {
			trays = append(trays, e)
		}
	}
	if len(trays) == len(f.Trays) {
		return nil
	}
	return writePrivateJSON(s.path, stateFile{Version: stateVersion, Trays: trays})
}

func (s *JoinState) load() stateFile {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return stateFile{Version: stateVersion}
	}
	var f stateFile
	if json.Unmarshal(data, &f) != nil || f.Version != stateVersion {
		return stateFile{Version: stateVersion}
	}
	return f
}

func writePrivateJSON(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return stateError(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o700); err != nil {
			return stateError(err)
		}
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return stateError(err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		_ = tmp.Close()
		return stateError(err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return stateError(err)
	}
	if err := tmp.Close(); err != nil {
		return stateError(err)
	}
	if err := os.Rename(name, path); err != nil {
		return stateError(err)
	}
	return nil
}

func stateError(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return &Error{Code: CodeState, Message: "cannot write the slicc config: " + pe.Op + ": " + pe.Err.Error()}
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return &Error{Code: CodeState, Message: "cannot write the slicc config: " + le.Op + ": " + le.Err.Error()}
	}
	return &Error{Code: CodeState, Message: "cannot write the slicc config"}
}

func (s *JoinState) ForgetHub(origin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := originKey(origin)
	hub := HubOrigin(origin)
	f := s.load()
	trays := make([]stateEntry, 0, len(f.Trays))
	for _, e := range f.Trays {
		if e.Origin != key && HubOrigin(e.JoinURL) != hub {
			trays = append(trays, e)
		}
	}
	if len(trays) == len(f.Trays) {
		return nil
	}
	return writePrivateJSON(s.path, stateFile{Version: stateVersion, Trays: trays})
}
