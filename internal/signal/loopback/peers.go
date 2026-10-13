package loopback

import (
	"encoding/json"
	"errors"
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

var ErrAmbiguous = errors.New("more than one paired seven matches")

func OpenPeers(dir string) *Peers { return &Peers{path: filepath.Join(dir, peersFileName)} }

func (p *Peers) Path() string { return p.path }

func (p *Peers) load() peersFile {
	f := peersFile{Version: stateVersion, Peers: map[string]Peer{}}
	data, err := os.ReadFile(p.path)
	if err != nil {
		return f
	}
	var got peersFile
	if json.Unmarshal(data, &got) != nil || got.Version != stateVersion || got.Peers == nil {
		return f
	}
	return got
}

func (p *Peers) Pin(fp, origin string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.load()
	if old, ok := f.Peers[fp]; ok && old.Origin == origin {
		return nil
	}
	f.Peers[fp] = Peer{Origin: origin, FirstSeen: time.Now().UTC().Format(time.RFC3339)}
	return writePrivateJSON(p.path, f)
}

func (p *Peers) Origin(fp string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.load().Peers[fp]
	return e.Origin, ok
}

func (p *Peers) List() []Peer {
	p.mu.Lock()
	defer p.mu.Unlock()
	return sorted(p.load().Peers)
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
	f := p.load()
	var hit []string
	for fp := range f.Peers {
		if prefix != "" && strings.HasPrefix(fp, prefix) {
			hit = append(hit, fp)
		}
	}
	switch len(hit) {
	case 0:
		return Peer{}, os.ErrNotExist
	case 1:
	default:
		return Peer{}, ErrAmbiguous
	}
	e := f.Peers[hit[0]]
	e.Fingerprint = hit[0]
	delete(f.Peers, hit[0])
	return e, writePrivateJSON(p.path, f)
}

func (p *Peers) RemoveAll() ([]Peer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.load()
	gone := sorted(f.Peers)
	if len(gone) == 0 {
		return nil, nil
	}
	return gone, writePrivateJSON(p.path, peersFile{Version: stateVersion, Peers: map[string]Peer{}})
}

func (p *Peers) Forget(fp string) (Peer, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.load()
	e, ok := f.Peers[fp]
	if !ok {
		return Peer{}, false, nil
	}
	e.Fingerprint = fp
	delete(f.Peers, fp)
	return e, true, writePrivateJSON(p.path, f)
}
