//go:build !wasip1

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	capcdp "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/lease"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
	"github.com/pion/webrtc/v4"
)

var leaseLabel = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func validateLeaseFollow(fs *flag.FlagSet, f followFlags) error {
	present := false
	bad := false
	fs.Visit(func(v *flag.Flag) {
		switch v.Name {
		case "lease-browser":
			present = true
		case "name", "json":
		default:
			bad = true
		}
	})
	if !present {
		return nil
	}
	if bad || len(f.compute.runner)+len(f.compute.shell)+len(f.compute.eval) > 0 || !leaseLabel.MatchString(f.name) || !capcdp.LocalEndpoint(f.leaseBrowser) {
		return errors.New("lease-only follow requires --name <label>, --lease-browser <literal loopback HTTP origin>, and no other sharing flags")
	}
	return nil
}

var errPinStore = errors.New("the link pin cannot be saved")

func pinPath(label string) (string, error) {
	base, e := os.UserConfigDir()
	if !leaseLabel.MatchString(label) {
		return "", errors.New("invalid local link label")
	}
	if e != nil {
		return "", errors.New("no user config directory")
	}
	return filepath.Join(base, "slicc", "lease-pins", label), nil
}
func normalizePin(text string) (string, error) {
	pin := strings.ToLower(strings.ReplaceAll(text, ":", ""))
	b, e := hex.DecodeString(pin)
	if e != nil || len(b) != 32 {
		return "", errors.New("expected full SHA-256 DTLS certificate fingerprint")
	}
	return pin, nil
}
func pairPin(label, fingerprint string) error {
	path, e := pinPath(label)
	if e != nil {
		return e
	}
	pin, e := normalizePin(fingerprint)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return errPinStore
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(e, os.ErrExist) {
		b, e := os.ReadFile(path)
		if e == nil && string(b) == pin {
			return nil
		}
		return errors.New("link label already pins a different peer; choose a new label")
	}
	if e != nil {
		return errPinStore
	}
	_, e = f.WriteString(pin)
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return errPinStore
	}
	return nil
}
func peerPinned(l *link.Link, pin string) bool {
	pc := l.PeerConnection()
	if pc.SCTP() == nil || pc.SCTP().Transport() == nil {
		return false
	}
	der := pc.SCTP().Transport().GetRemoteCertificate()
	h := sha256.Sum256(der)
	return len(der) > 0 && hex.EncodeToString(h[:]) == pin
}
func (r *followRun) openStores() error {
	var err error
	if r.state, err = tray.OpenDefaultState(); err != nil {
		return errors.New("no user config directory")
	}
	if r.certs, err = tray.OpenDefaultCerts(); err != nil {
		return errors.New("no user config directory")
	}
	return nil
}

func (r *followRun) leaseIsolation(ff followFlags, logf func(string, ...any)) (func(string, ...any), error) {
	if ff.leaseBrowser != "" {
		r.state = nil
		r.certs = leaseCertStore(r.certs.Path())
		r.leaseCert = true
		if err := r.claimLeaseDevice(); err != nil {
			return nil, err
		}
		return func(string, ...any) {}, nil
	}
	return logf, nil
}
func (r *followRun) requireLeaseDevice() error {
	if want := os.Getenv("SLICC_LEASE_DEVICE"); want != "" && r.device != want {
		if r.leaseClaim != nil {
			_ = r.leaseClaim.Close()
			r.leaseClaim = nil
		}
		return errLeaseDeviceMismatch
	}
	return nil
}
func (r *followRun) startLeaseOnly(ff followFlags, st *stopper, cert webrtc.Certificate) (*followRun, error) {
	if err := r.requireLeaseDevice(); err != nil {
		return nil, err
	}
	return r.leaseOnly(ff, st, cert)
}
func (r *followRun) leaseOnly(ff followFlags, st *stopper, cert webrtc.Certificate) (*followRun, error) {
	path, e := pinPath(ff.name)
	if e != nil {
		return nil, e
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		return nil, errors.New("no verified Seven fingerprint for this label; use slicc lease pair")
	}
	pin, e := normalizePin(string(raw))
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, e := lease.New(ctx, ff.leaseBrowser, ff.name, []string{r.hub})
	if e != nil {
		return nil, e
	}
	r.lease = b
	r.node = node.New(node.Config{Name: ff.name, Host: ff.name, Version: version, Mode: "remote", Certificates: []webrtc.Certificate{cert}})
	r.node.Handle(lease.Cap, lease.Cap, func(s *link.Stream) {
		if !peerPinned(s.Link(), pin) {
			_ = s.Reset("E_AUTH")
			return
		}
		b.Serve(s)
	})
	r.node.OnHello(func(h *link.Hello) { h.Routes = nil; h.Policy = nil })
	r.node.Subscribe(func(ev node.Event) {
		if ev.Kind == node.EventConnecting {
			go func() {
				select {
				case <-ev.Link.Ready():
					if !peerPinned(ev.Link, pin) {
						_ = ev.Link.Close()
						if conn := r.currentConn(); conn != nil {
							r.emit(followEvent{kind: evFailed, conn: conn, nonce: conn.Session().BootstrapID(), err: errors.New("paired fingerprint mismatch")})
						}
						return
					}
					r.onLink(ev.Link)
				case <-ev.Link.Done():
					if conn := r.currentConn(); conn != nil {
						r.emit(followEvent{kind: evFailed, conn: conn, nonce: conn.Session().BootstrapID(), err: errors.New("lease peer did not establish an exclusive link")})
					}
				}
			}()
		}
	})
	h := ctl.NewHolder("follow", version)
	st.register(h)
	h.Update(func(s *ctl.Status) { s.Device = r.device; s.Hub = r.hub; s.Offers = []string{lease.Cap} })
	leaseControl(h, b)
	r.ctl = h
	srv, e := ctl.Serve(h)
	if e != nil {
		b.Close()
		return nil, e
	}
	go func() { <-b.Done(); st.stop() }()
	r.closeSSH = func() { b.Close(); srv.Close() }
	return r, nil
}
func leaseControl(h *ctl.Holder, b *lease.Broker) {
	h.Answer(ctl.OpLeaseList, func(ctl.Request) map[string]any {
		s := b.List()
		return map[string]any{"ok": true, "pending": s.Pending, "active": s.Active}
	})
	for _, op := range []string{ctl.OpLeaseApprove, ctl.OpLeaseDeny, ctl.OpLeaseRevoke} {
		h.Answer(op, func(req ctl.Request) map[string]any {
			var e error
			switch op {
			case ctl.OpLeaseApprove:
				e = b.Approve(req.Request)
			case ctl.OpLeaseDeny:
				e = b.Deny(req.Request)
			default:
				e = b.Revoke(req.Lease)
			}
			if e != nil {
				return map[string]any{"ok": false, "error": e.Error()}
			}
			return map[string]any{"ok": true}
		})
	}
}
