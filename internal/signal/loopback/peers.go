package loopback

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Peer struct {
	Fingerprint string `json:"-"`
	Origin      string `json:"origin"`
	FirstSeen   string `json:"firstSeen"`
}

type peersFile struct {
	Version int             `json:"version"`
	Peers   map[string]Peer `json:"peers"`
}

type Peers struct {
	path string
	mu   sync.Mutex
}

var (
	ErrAmbiguous = errors.New("more than one paired seven matches")
	ErrLocked    = errors.New("the loopback state is being changed by another slicc; try again")
)

const lockWait = 5 * time.Second

func (p *Peers) lock() (func(), error) { return lockFile(p.path + ".lock") }

func lockFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		ok, err := tryLock(f)
		if ok {
			return func() { unlock(f); _ = f.Close() }, nil
		}
		if err != nil || time.Now().After(deadline) {
			_ = f.Close()
			if err == nil {
				err = ErrLocked
			}
			return nil, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (p *Peers) update(f func(*peersFile) (bool, error)) error {
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	pf, err := p.load()
	if err != nil {
		return err
	}
	changed, err := f(&pf)
	if err != nil || !changed {
		return err
	}
	return writePrivateJSON(p.path, pf)
}

func OpenPeers(dir string) *Peers { return &Peers{path: filepath.Join(dir, peersFileName)} }

func (p *Peers) Path() string { return p.path }

func (p *Peers) load() (peersFile, error) {
	f := peersFile{Version: stateVersion, Peers: map[string]Peer{}}
	data, err := os.ReadFile(p.path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	var got peersFile
	if json.Unmarshal(data, &got) != nil || got.Version != stateVersion {
		return f, fmt.Errorf("%s is not a list of paired sevens slicc can read; slicc leaves it alone, move it away to start over", p.path)
	}
	if got.Peers == nil {
		got.Peers = map[string]Peer{}
	}
	return got, nil
}

func (p *Peers) Pin(fp, origin string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.update(func(f *peersFile) (bool, error) {
		if old, ok := f.Peers[fp]; ok && old.Origin == origin {
			return false, nil
		}
		f.Peers[fp] = Peer{Origin: origin, FirstSeen: time.Now().UTC().Format(time.RFC3339)}
		return true, nil
	})
}

func (p *Peers) Origin(fp string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, err := p.load()
	if err != nil {
		return "", false
	}
	e, ok := f.Peers[fp]
	return e.Origin, ok
}

func (p *Peers) List() ([]Peer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, err := p.load()
	return sorted(f.Peers), err
}

func sorted(m map[string]Peer) []Peer {
	out := make([]Peer, 0, len(m))
	for fp, e := range m {
		e.Fingerprint = fp
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FirstSeen != out[j].FirstSeen {
			return out[i].FirstSeen < out[j].FirstSeen
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})
	return out
}

func (p *Peers) Remove(prefix string) (Peer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	prefix = NormalizeFingerprint(strings.ReplaceAll(prefix, "…", ""))
	var e Peer
	err := p.update(func(f *peersFile) (bool, error) {
		var hit []string
		for fp := range f.Peers {
			if prefix != "" && strings.HasPrefix(fp, prefix) {
				hit = append(hit, fp)
			}
		}
		switch len(hit) {
		case 0:
			return false, os.ErrNotExist
		case 1:
		default:
			return false, ErrAmbiguous
		}
		e = f.Peers[hit[0]]
		e.Fingerprint = hit[0]
		delete(f.Peers, hit[0])
		return true, nil
	})
	return e, err
}

func (p *Peers) RemoveAll() ([]Peer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var gone []Peer
	err := p.update(func(f *peersFile) (bool, error) {
		gone = sorted(f.Peers)
		f.Peers = map[string]Peer{}
		return len(gone) > 0, nil
	})
	return gone, err
}

func (p *Peers) Forget(fp string) (Peer, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var e Peer
	var ok bool
	err := p.update(func(f *peersFile) (bool, error) {
		e, ok = f.Peers[fp]
		if !ok {
			return false, nil
		}
		e.Fingerprint = fp
		delete(f.Peers, fp)
		return true, nil
	})
	return e, ok, err
}
