//go:build !wasip1

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/flock"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
	"github.com/pion/webrtc/v4"
	"golang.org/x/net/idna"
)

const leaseCertSuffix = ".lease"

var errHubOrigin = errors.New("expected the tray hub's https origin (http only on a loopback host), without a path, query, fragment or user name")

func leaseCertStore(base string) *tray.Certs { return tray.OpenCerts(base + leaseCertSuffix) }

func reviewHubOrigin(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" && (u.Scheme != "http" || !loopbackHost(u.Hostname())) || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "#?") || u.Path != "" && u.Path != "/" {
		return "", errHubOrigin
	}
	origin, ok := canonOrigin(u.Scheme, u.Host)
	if !ok {
		return "", errHubOrigin
	}
	return origin, nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func joinHubOrigin(joinURL string) string {
	u, e := tray.ParseJoinURL(joinURL)
	if e != nil {
		return ""
	}
	origin, _ := canonOrigin(strings.ToLower(u.Scheme), u.Host)
	return origin
}

func leaseCertKey(hub string) string { return tray.HubOrigin(hub + "/join/key") }

func sameLeaseHub(joinURL, hub string) bool {
	origin := joinHubOrigin(joinURL)
	return origin != "" && origin == hub
}

func canonOrigin(scheme, hostport string) (string, bool) {
	u := &url.URL{Host: hostport}
	host, port := u.Hostname(), u.Port()
	if strings.HasSuffix(hostport, ":") {
		return "", false
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", false
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
		if ip.To4() == nil {
			host = "[" + host + "]"
		}
	} else if strings.ContainsAny(host, "[]:") {
		return "", false
	} else {
		a, e := idna.Lookup.ToASCII(host)
		if e != nil || a == "" || strings.HasSuffix(a, ".") {
			return "", false
		}
		host = a
	}
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", false
		}
		if scheme == "https" && n == 443 || scheme == "http" && n == 80 {
			port = ""
		}
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, true
}

func leaseStoreMsg(err error) error {
	switch {
	case errors.Is(err, tray.ErrCertUnreadable):
		return errors.New("the lease device certificate cannot be read")
	case errors.Is(err, tray.ErrCertDamaged):
		return errors.New("the lease device certificate is damaged")
	default:
		return err
	}
}

var (
	errLeaseSecure = errors.New("the lease device certificate cannot be secured")
	errLeaseRead   = errors.New("the lease device certificate cannot be read")
)

func openLeaseFile(path string, flag int, perm fs.FileMode) (*os.File, error) {
	if runtime.GOOS == "windows" {
		if info, e := os.Lstat(path); e == nil && info.Mode()&fs.ModeSymlink != 0 {
			return nil, errLeaseSecure
		}
	}
	f, e := os.OpenFile(path, flag|leaseNoFollow, perm)
	if e != nil {
		return nil, e
	}
	info, e := f.Stat()
	if e != nil {
		_ = f.Close()
		return nil, e
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("not a regular file")
	}
	return f, nil
}

func tightenLeaseFile(f *os.File) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, e := f.Stat()
	if e != nil {
		return errLeaseSecure
	}
	perm := info.Mode().Perm()
	if perm&0o600 != 0o600 {
		return errLeaseRead
	}
	tight := perm & 0o600
	if tight != perm && f.Chmod(tight) != nil {
		return errLeaseSecure
	}
	return nil
}

func leaseOwnerOnly(path string, dir bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errLeaseSecure
	}
	mode := info.Mode()
	if mode&fs.ModeSymlink != 0 || dir && !mode.IsDir() || !dir && !mode.IsRegular() {
		return errLeaseSecure
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	want := fs.FileMode(0o600)
	need := fs.FileMode(0o600)
	if dir {
		want = 0o700
		need = 0o700
	}
	perm := mode.Perm()
	if perm&need != need {
		return errLeaseRead
	}
	if perm&^want == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDONLY|leaseNoFollow, 0)
	if err != nil {
		return errLeaseSecure
	}
	defer func() { _ = f.Close() }()
	cur, err := f.Stat()
	if err != nil || !os.SameFile(info, cur) {
		return errLeaseSecure
	}
	tight := cur.Mode().Perm() & want
	if tight&need != need {
		return errLeaseRead
	}
	if f.Chmod(tight) != nil {
		return errLeaseSecure
	}
	return nil
}

func withLeaseLock(certs *tray.Certs, fn func() error) error {
	dir := filepath.Dir(certs.Path())
	if e := os.MkdirAll(dir, 0o700); e != nil {
		return errLeaseSecure
	}
	if e := leaseOwnerOnly(dir, true); e != nil {
		return e
	}
	lock, e := openLeaseFile(certs.Path()+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if e != nil {
		return errLeaseSecure
	}
	defer func() { _ = lock.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ok, e := flock.TryLock(lock)
		if ok {
			break
		}
		if e != nil || time.Now().After(deadline) {
			return errors.New("the lease device certificate is locked")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer flock.Unlock(lock)
	if e := tightenLeaseFile(lock); e != nil {
		return e
	}
	if e := leaseOwnerOnly(certs.Path(), false); e != nil {
		return e
	}
	return fn()
}

func syncLeaseStore(path string) error {
	f, e := os.OpenFile(path, os.O_RDWR|leaseNoFollow, 0)
	if runtime.GOOS == "windows" {
		if e == nil {
			_ = f.Sync()
			_ = f.Close()
		}
		return nil
	}
	if e != nil {
		return e
	}
	e = f.Sync()
	_ = f.Close()
	if e != nil {
		return e
	}
	if d, e := os.Open(filepath.Dir(path)); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func leaseIdentity(certs *tray.Certs, origin string) (string, bool, error) {
	var pemData string
	var created bool
	e := withLeaseLock(certs, func() error {
		var err error
		pemData, created, err = certs.LoadLease(origin)
		if err != nil {
			return leaseStoreMsg(err)
		}
		if created {
			if err := syncLeaseStore(certs.Path()); err != nil {
				return errors.New("the lease device certificate cannot be secured")
			}
		}
		return nil
	})
	return pemData, created, e
}

func (r *followRun) leaseCertMutate(fn func(*tray.Certs) error) error {
	if !r.leaseCert {
		return fn(r.certs)
	}
	return withLeaseLock(r.certs, func() error { return fn(r.certs) })
}

func pemIdentity(pemData string) (*webrtc.Certificate, webrtc.DTLSFingerprint, error) {
	cert, e := webrtc.CertificateFromPEM(pemData)
	if e != nil {
		return nil, webrtc.DTLSFingerprint{}, fmt.Errorf("the device certificate: %w", e)
	}
	fps, e := cert.GetFingerprints()
	if e != nil || len(fps) == 0 {
		return nil, webrtc.DTLSFingerprint{}, errors.New("the device certificate has no fingerprint")
	}
	return cert, fps[0], nil
}

func deviceHex(fp webrtc.DTLSFingerprint) string {
	return strings.ToLower(strings.ReplaceAll(fp.Value, ":", ""))
}

func leaseDevice(hub string) (string, error) {
	base, e := tray.DefaultCertsPath()
	if e != nil {
		return "", errors.New("no user config directory")
	}
	pemData, _, e := leaseIdentity(leaseCertStore(base), leaseCertKey(hub))
	if e != nil {
		return "", e
	}
	_, fp, e := pemIdentity(pemData)
	if e != nil {
		return "", e
	}
	return deviceHex(fp), nil
}

func (r *followRun) loadCert() (string, bool, error) {
	if !r.leaseCert {
		return r.certs.Load(r.hub)
	}
	return leaseIdentity(r.certs, r.hub)
}
