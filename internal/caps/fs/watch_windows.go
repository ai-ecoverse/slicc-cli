//go:build windows

package fs

import (
	"errors"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const watchMask = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME |
	windows.FILE_NOTIFY_CHANGE_ATTRIBUTES | windows.FILE_NOTIFY_CHANGE_SIZE |
	windows.FILE_NOTIFY_CHANGE_LAST_WRITE | windows.FILE_NOTIFY_CHANGE_CREATION | windows.FILE_NOTIFY_CHANGE_SECURITY

type dirWatch struct {
	root   string
	h      windows.Handle
	ev     windows.Handle
	quit   windows.Handle
	buf    []uint64
	exited chan struct{}
}

func startBackend(root string, note func(rel string, known bool), lost func()) (func(), error) {
	h, err := createFile(root, windows.FILE_LIST_DIRECTORY, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED)
	if err != nil {
		return nil, err
	}
	w := &dirWatch{root: root, h: h, buf: make([]uint64, 8192), exited: make(chan struct{})}
	if w.ev, err = windows.CreateEvent(nil, 1, 0, nil); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	if w.quit, err = windows.CreateEvent(nil, 1, 0, nil); err != nil {
		_ = windows.CloseHandle(w.ev)
		_ = windows.CloseHandle(h)
		return nil, err
	}
	go w.loop(note, lost)
	return func() {
		_ = windows.SetEvent(w.quit)
		<-w.exited
		_ = windows.CloseHandle(w.ev)
		_ = windows.CloseHandle(w.quit)
		_ = windows.CloseHandle(w.h)
	}, nil
}

func (w *dirWatch) loop(note func(string, bool), lost func()) {
	defer close(w.exited)
	bytes := unsafe.Slice((*byte)(unsafe.Pointer(&w.buf[0])), len(w.buf)*8)
	for {
		ov := &windows.Overlapped{HEvent: w.ev}
		_ = windows.ResetEvent(w.ev)
		if err := windows.ReadDirectoryChanges(w.h, &bytes[0], uint32(len(bytes)), true, watchMask, nil, ov, 0); err != nil {
			lost()
			return
		}
		r, err := w.wait()
		if err != nil || r != windows.WAIT_OBJECT_0 {
			_ = windows.CancelIoEx(w.h, ov)
			var n uint32
			_ = windows.GetOverlappedResult(w.h, ov, &n, true)
			if r != windows.WAIT_OBJECT_0+1 {
				lost()
			}
			return
		}
		var n uint32
		if err := windows.GetOverlappedResult(w.h, ov, &n, false); err != nil {
			if errors.Is(err, windows.ERROR_NOTIFY_ENUM_DIR) {
				note("", false)
				continue
			}
			lost()
			return
		}
		if n == 0 {
			note("", false)
			continue
		}
		parseNotify(bytes[:n], note)
	}
}

func (w *dirWatch) wait() (uint32, error) {
	for {
		r, err := windows.WaitForMultipleObjects([]windows.Handle{w.ev, w.quit}, false, 1000)
		if err != nil || r != uint32(windows.WAIT_TIMEOUT) {
			return r, err
		}
		if !w.inPlace() {
			return r, nil
		}
	}
}

func (w *dirWatch) inPlace() bool {
	buf := make([]uint16, 512)
	n, err := windows.GetFinalPathNameByHandle(w.h, &buf[0], uint32(len(buf)), 0)
	if err != nil || int(n) >= len(buf) {
		return err == nil
	}
	return windows.UTF16ToString(buf[:n]) == w.root
}

func parseNotify(b []byte, note func(string, bool)) {
	for off := uint32(0); int(off)+12 <= len(b); {
		fi := (*windows.FileNotifyInformation)(unsafe.Pointer(&b[off]))
		start := int(off) + 12
		end := start + int(fi.FileNameLength)
		if end > len(b) {
			return
		}
		name := windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&b[start])), fi.FileNameLength/2))
		rel := filepath.ToSlash(strings.ReplaceAll(name, `\`, "/"))
		if rel == "" {
			note("", false)
		} else {
			note(rel, true)
		}
		if fi.NextEntryOffset == 0 {
			return
		}
		off += fi.NextEntryOffset
	}
}
