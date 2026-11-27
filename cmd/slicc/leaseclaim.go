//go:build !wasip1

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/flock"
	"github.com/ai-ecoverse/slicc-cli/internal/lease"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

const (
	leaseClaimBusyExit      = 3
	leaseDeviceMismatchExit = 4
	leaseClaimErrorExit     = 5
)

var (
	errLeaseClaimHeld      = errors.New("a lease-only link appeared during the review; it was left alone")
	errLeaseDeviceMismatch = errors.New("the started broker reports a different fingerprint than the one shown")
	errLeaseClaimFailed    = errors.New("the lease device claim cannot be taken")
)

func leaseClaimFile(store, origin string) string {
	sum := sha256.Sum256([]byte(origin))
	return store + ".claim." + hex.EncodeToString(sum[:])
}

func (r *followRun) claimLeaseDevice() error {
	origin := tray.HubOrigin(r.joinURL)
	if origin == "" {
		return errors.New("invalid join URL")
	}
	pemData, _, err := leaseIdentity(r.certs, r.hub)
	if err != nil {
		return err
	}
	_, fp, err := pemIdentity(pemData)
	if err != nil {
		return err
	}
	device := deviceHex(fp)
	path := leaseClaimFile(r.certs.Path(), origin)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errLeaseClaimFailed
	}
	f, err := openLeaseFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return errLeaseClaimFailed
	}
	if err := tightenLeaseFile(f); err != nil {
		_ = f.Close()
		return errLeaseClaimFailed
	}
	ok, err := flock.TryLock(f)
	if err != nil {
		_ = f.Close()
		return errLeaseClaimFailed
	}
	if !ok {
		_ = f.Close()
		return errLeaseClaimHeld
	}
	if err := leaseDeviceBusy(device, os.Getpid()); err != nil {
		flock.Unlock(f)
		_ = f.Close()
		return err
	}
	r.leaseClaim = f
	return nil
}

func leaseDeviceBusy(device string, self int) error { return leaseSlotFree(device, self) }

func leaseLocal(ctx context.Context, fn func(pid int, st *ctl.Status) error) error {
	dir, err := ctl.Dir()
	if err != nil {
		return err
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.sock"))
	if err != nil {
		return err
	}
	type found struct {
		pid int
		st  *ctl.Status
	}
	rows := make([]*found, len(matches))
	var wg sync.WaitGroup
	for i, path := range matches {
		pid, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(path), ".sock"))
		if err != nil || pid <= 0 {
			continue
		}
		wg.Add(1)
		go func(i int, path string, pid int) {
			defer wg.Done()
			if st, err := ctl.QueryPID(ctx, path, pid); err == nil {
				rows[i] = &found{pid, st}
			}
		}(i, path, pid)
	}
	wg.Wait()
	for _, row := range rows {
		if row == nil {
			continue
		}
		if err := fn(row.pid, row.st); err != nil {
			return err
		}
	}
	return nil
}

func leaseSlotFree(pin string, self int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return leaseLocal(ctx, func(pid int, st *ctl.Status) error {
		if pid == self {
			return nil
		}
		if st.Device == pin || slices.Equal(st.Offers, []string{lease.Cap}) {
			return errLeaseClaimHeld
		}
		return nil
	})
}
