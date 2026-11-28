//go:build !windows

package tray

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"
)

func blockingSave(t *testing.T, path string) (reached chan *os.File) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	reached = make(chan *os.File, 1)
	stop, exited := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		<-exited
		select {
		case w, ok := <-reached:
			if ok {
				_ = w.Close()
			}
		default:
		}
	})
	go func() {
		defer close(exited)
		for {
			if w, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				reached <- w
				return
			}
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	return reached
}

func awaitSave(t *testing.T, reached chan *os.File) *os.File {
	t.Helper()
	select {
	case w, ok := <-reached:
		if !ok {
			t.Fatal("the store's blocking read could not be joined")
		}
		t.Cleanup(func() { _ = w.Close() })
		return w
	case <-time.After(10 * time.Second):
		t.Fatal("the worker never reached the store's Save")
		return nil
	}
}

func TestOrdinaryFollowUnchanged(t *testing.T) {
	t.Run("DialPublishesBeforeSave", func(t *testing.T) {
		chain := sameChain(1)
		path := filepath.Join(t.TempDir(), "join.json")
		state := OpenState(path)
		f, err := NewFollower(Config{JoinURL: chain[0], State: state, HTTPClient: &http.Client{Transport: newPinWorld(chainRoutes(chain, "location"))}})
		if err != nil {
			t.Fatal(err)
		}
		reached := blockingSave(t, path)
		done := make(chan error, 1)
		go func() {
			s, err := f.Dial(t.Context())
			if s != nil {
				_ = s.Close()
			}
			done <- err
		}()
		w := awaitSave(t, reached)
		published := f.currentURL() == chain[1]
		_ = w.Close()
		if err := <-done; err != nil || !published {
			t.Fatalf("the successor was not current while its save was pending: published=%v %v", published, err)
		}
		if state.Current(chain[0]) != chain[1] {
			t.Fatal("the successor was not saved")
		}
	})
	t.Run("RejoinSavesBeforePublish", func(t *testing.T) {
		typed, next := pinJoin(pinHub, "typed"), pinJoin(pinHub, "next")
		path := filepath.Join(t.TempDir(), "join.json")
		state := OpenState(path)
		f, err := NewFollower(Config{JoinURL: typed, State: state})
		if err != nil {
			t.Fatal(err)
		}
		reached := blockingSave(t, path)
		done := make(chan error, 1)
		go func() { done <- f.Rejoin(next) }()
		w := awaitSave(t, reached)
		early := f.currentURL() != typed
		_ = w.Close()
		if err := <-done; err != nil || early || f.currentURL() != next || state.Current(typed) != next {
			t.Fatalf("Rejoin published before its save returned: early=%v %v", early, err)
		}
	})
}

func TestLeaseFollowerLatchOrdering(t *testing.T) {
	typed, same, foreign := pinJoin(pinHub, "typed"), pinJoin(pinHub, "same"), pinJoin(pinForeign, "far")
	pinned := func(t *testing.T, routes map[string]pinRoute) (*Follower, *pinWorld, string, func() []string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "join.json")
		var mu sync.Mutex
		var changed []string
		w := newPinWorld(routes)
		f, err := NewFollower(Config{JoinURL: typed, PinnedJoinURL: typed, State: OpenState(path), HTTPClient: &http.Client{Transport: w}, OnJoinURLChanged: func(u string) {
			mu.Lock()
			defer mu.Unlock()
			changed = append(changed, u)
		}})
		if err != nil {
			t.Fatal(err)
		}
		return f, w, path, func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(changed)
		}
	}
	t.Run("RejoinSaveOverlapsRefusal", func(t *testing.T) {
		f, _, path, changes := pinned(t, nil)
		reached := blockingSave(t, path)
		done := make(chan error, 1)
		go func() { done <- f.Rejoin(same) }()
		w := awaitSave(t, reached)
		if err := f.Rejoin(foreign); ErrorCode(err) != CodeSupersededForeignOrigin {
			t.Fatalf("Rejoin: %v", err)
		}
		_ = w.Close()
		if err := <-done; ErrorCode(err) != CodeSupersededForeignOrigin || f.currentURL() != typed || len(changes()) != 0 {
			t.Fatalf("the overlapping Rejoin published after the refusal: %v current moved=%v callbacks=%d", err, f.currentURL() != typed, len(changes()))
		}
	})
	t.Run("DialSaveOverlapsRefusal", func(t *testing.T) {
		f, w, path, changes := pinned(t, map[string]pinRoute{typed: {"location", same}, same: {}})
		reached := blockingSave(t, path)
		done := make(chan error, 1)
		go func() {
			s, err := f.Dial(t.Context())
			if s != nil {
				_ = s.Close()
			}
			done <- err
		}()
		fifo := awaitSave(t, reached)
		if err := f.Rejoin(foreign); ErrorCode(err) != CodeSupersededForeignOrigin {
			t.Fatalf("Rejoin: %v", err)
		}
		_ = fifo.Close()
		if err := <-done; ErrorCode(err) != CodeSupersededForeignOrigin || len(changes()) != 0 || !slices.Equal(w.requests(), []string{pinKeyOf(typed)}) {
			t.Fatalf("the overlapping Dial carried on after the refusal: %v callbacks=%v requests=%v", err, changes(), w.requests())
		}
	})
}
