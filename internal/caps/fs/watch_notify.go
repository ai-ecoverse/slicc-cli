//go:build !windows && !darwin

package fs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/fsnotify/fsnotify"
)

func fatalWatch(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) || errors.Is(err, syscall.ENOMEM)
}

func addTree(w *fsnotify.Watcher, dir string, top bool) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if top && p == dir {
				return err
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if err := w.Add(p); err != nil && ((top && p == dir) || fatalWatch(err)) {
			return err
		}
		return nil
	})
}

func startBackend(root string, note func(rel string, known bool), lost func()) (func(), error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := addTree(w, root, true); err != nil {
		_ = w.Close()
		return nil, err
	}
	done := make(chan struct{})
	go notifyLoop(w, root, done, note, lost)
	return func() {
		close(done)
		_ = w.Close()
	}, nil
}

func notifyLoop(w *fsnotify.Watcher, root string, done <-chan struct{}, note func(string, bool), lost func()) {
	for {
		select {
		case <-done:
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if !notifyEvent(w, root, ev, note) {
				lost()
				return
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			if !errors.Is(err, fsnotify.ErrEventOverflow) {
				lost()
				return
			}
			note("", false)
		}
	}
}

func notifyEvent(w *fsnotify.Watcher, root string, ev fsnotify.Event, note func(string, bool)) bool {
	if ev.Name == root && ev.Has(fsnotify.Remove|fsnotify.Rename) {
		return false
	}
	if ev.Has(fsnotify.Create) {
		if fi, err := os.Lstat(ev.Name); err == nil && fi.IsDir() && addTree(w, ev.Name, false) != nil {
			return false
		}
	}
	note(relName(root, ev.Name))
	return true
}
