//go:build darwin

package fs

import (
	"errors"
	"os"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	fseNoDefer         = 0x02
	fseWatchRoot       = 0x04
	fseFileEvents      = 0x10
	fseMustScanSubDirs = 0x01
	fseUserDropped     = 0x02
	fseKernelDropped   = 0x04
	fseRootChanged     = 0x20
	fseSinceNow        = ^uint64(0)
	cfStringUTF8       = 0x08000100
)

type fseContext struct {
	_    uintptr
	info uintptr
	_    [3]uintptr
}

type fseStream struct {
	root string
	note func(string, bool)
	lost func()
	once sync.Once
}

var fse struct {
	once    sync.Once
	err     error
	cfArray uintptr

	cfStringCreate func(alloc uintptr, s *byte, enc uint32) uintptr
	cfArrayCreate  func(alloc uintptr, values *uintptr, n int, callbacks uintptr) uintptr
	cfRelease      func(uintptr)
	create         func(alloc, cb uintptr, ctx *fseContext, paths uintptr, since uint64, latency float64, flags uint32) uintptr
	setQueue       func(stream, queue uintptr)
	start          func(stream uintptr) bool
	stop           func(stream uintptr)
	invalidate     func(stream uintptr)
	release        func(stream uintptr)
	queueCreate    func(label *byte, attr uintptr) uintptr
	dispatchFree   func(obj uintptr)
	callback       uintptr

	mu      sync.Mutex
	next    uintptr
	streams map[uintptr]*fseStream
}

func fseLoad() error {
	fse.once.Do(func() {
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			fse.err = err
			return
		}
		cs, err := purego.Dlopen("/System/Library/Frameworks/CoreServices.framework/CoreServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			fse.err = err
			return
		}
		ds, err := purego.Dlopen("/usr/lib/system/libdispatch.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			fse.err = err
			return
		}
		if fse.cfArray, err = purego.Dlsym(cf, "kCFTypeArrayCallBacks"); err != nil {
			fse.err = err
			return
		}
		purego.RegisterLibFunc(&fse.cfStringCreate, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&fse.cfArrayCreate, cf, "CFArrayCreate")
		purego.RegisterLibFunc(&fse.cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&fse.create, cs, "FSEventStreamCreate")
		purego.RegisterLibFunc(&fse.setQueue, cs, "FSEventStreamSetDispatchQueue")
		purego.RegisterLibFunc(&fse.start, cs, "FSEventStreamStart")
		purego.RegisterLibFunc(&fse.stop, cs, "FSEventStreamStop")
		purego.RegisterLibFunc(&fse.invalidate, cs, "FSEventStreamInvalidate")
		purego.RegisterLibFunc(&fse.release, cs, "FSEventStreamRelease")
		purego.RegisterLibFunc(&fse.queueCreate, ds, "dispatch_queue_create")
		purego.RegisterLibFunc(&fse.dispatchFree, ds, "dispatch_release")
		fse.streams = map[uintptr]*fseStream{}
		fse.callback = purego.NewCallback(fseCallback)
	})
	return fse.err
}

func cString(s string) *byte {
	b := append([]byte(s), 0)
	return &b[0]
}

func goString(p unsafe.Pointer) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}

func fseCallback(_, info, n uintptr, paths, flags unsafe.Pointer, _ uintptr) {
	fse.mu.Lock()
	st := fse.streams[info]
	fse.mu.Unlock()
	if st == nil || n == 0 {
		return
	}
	pathv := unsafe.Slice((*unsafe.Pointer)(paths), n)
	flagv := unsafe.Slice((*uint32)(flags), n)
	for i := range pathv {
		f := flagv[i]
		name := goString(pathv[i])
		switch {
		case f&fseRootChanged != 0:
			st.once.Do(st.lost)
			return
		case f&(fseMustScanSubDirs|fseUserDropped|fseKernelDropped) != 0:
			st.note("", false)
		default:
			st.note(darwinRel(st.root, name))
		}
	}
}

func darwinRel(root, name string) (string, bool) {
	if rel, ok := relName(root, name); ok {
		return rel, true
	}
	if len(name) > len(root) && name[len(root)] == '/' && strings.EqualFold(name[:len(root)], root) {
		return name[len(root)+1:], true
	}
	return "", false
}

func startBackend(root string, note func(rel string, known bool), lost func()) (func(), error) {
	if err := fseLoad(); err != nil {
		return nil, err
	}
	fi, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, errors.New("fs: the watched root is not a directory")
	}
	fse.mu.Lock()
	fse.next++
	id := fse.next
	fse.streams[id] = &fseStream{root: root, note: note, lost: lost}
	fse.mu.Unlock()
	forget := func() {
		fse.mu.Lock()
		delete(fse.streams, id)
		fse.mu.Unlock()
	}
	path := fse.cfStringCreate(0, cString(root), cfStringUTF8)
	if path == 0 {
		forget()
		return nil, errors.New("fs: CFStringCreateWithCString failed")
	}
	paths := fse.cfArrayCreate(0, &path, 1, fse.cfArray)
	fse.cfRelease(path)
	if paths == 0 {
		forget()
		return nil, errors.New("fs: CFArrayCreate failed")
	}
	ctx := fseContext{info: id}
	stream := fse.create(0, fse.callback, &ctx, paths, fseSinceNow, 0.01, fseNoDefer|fseWatchRoot|fseFileEvents)
	fse.cfRelease(paths)
	if stream == 0 {
		forget()
		return nil, errors.New("fs: FSEventStreamCreate failed")
	}
	queue := fse.queueCreate(cString("ai.sliccy.slicc.fs"), 0)
	fse.setQueue(stream, queue)
	if !fse.start(stream) {
		fse.invalidate(stream)
		fse.release(stream)
		fse.dispatchFree(queue)
		forget()
		return nil, errors.New("fs: FSEventStreamStart failed")
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			forget()
			fse.stop(stream)
			fse.invalidate(stream)
			fse.release(stream)
			fse.dispatchFree(queue)
		})
	}, nil
}
