package screen

import (
	"net"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

type securityAPI struct {
	pidInfo      func(int32, int32, uint64, *int32, int32) int32
	typeID       func(uintptr) uint64
	stringType   func() uint64
	dictType     func() uint64
	numberType   func() uint64
	boolType     func() uint64
	sockopt      func(int32, int32, int32, *uint32, *uint32) int32
	release      func(uintptr)
	dataCreate   func(uintptr, *byte, int64) uintptr
	dictCreate   func(uintptr, *uintptr, *uintptr, int64, uintptr, uintptr) uintptr
	strCreate    func(uintptr, *byte, uint32) uintptr
	strGet       func(uintptr, *byte, int64, uint32) bool
	dictGet      func(uintptr, uintptr) uintptr
	numberGet    func(uintptr, int32, *int64) bool
	boolGet      func(uintptr) bool
	guestCopy    func(uintptr, uintptr, uint32, *uintptr) int32
	reqCreate    func(uintptr, uint32, *uintptr) int32
	check        func(uintptr, uint32, uintptr) int32
	staticCopy   func(uintptr, uint32, *uintptr) int32
	staticCheck  func(uintptr, uint32, uintptr) int32
	infoCopy     func(uintptr, uint32, *uintptr) int32
	audit        uintptr
	flags        uintptr
	plist        uintptr
	entitlements uintptr
}

var security struct {
	once sync.Once
	api  securityAPI
	err  error
}

func NativeAvailable() bool { return true }
func loadSecurity() (api *securityAPI, err error) {
	security.once.Do(func() { security.err = security.api.load() })
	return &security.api, security.err
}
func (a *securityAPI) load() error { return a.loadUsing(purego.Dlopen, purego.Dlsym) }
func (a *securityAPI) loadUsing(open func(string, int) (uintptr, error), lookup func(uintptr, string) (uintptr, error)) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrAccess
		}
	}()
	cf, e := open("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if e != nil {
		return ErrAccess
	}
	sec, e := open("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if e != nil {
		return ErrAccess
	}
	bind := func(h uintptr, name string, fn any) bool {
		p, e := lookup(h, name)
		if e != nil || p == 0 {
			return false
		}
		purego.RegisterFunc(fn, p)
		return true
	}
	for _, v := range []struct {
		name string
		fn   any
	}{{"CFGetTypeID", &a.typeID}, {"CFStringGetTypeID", &a.stringType}, {"CFDictionaryGetTypeID", &a.dictType}, {"CFNumberGetTypeID", &a.numberType}, {"CFBooleanGetTypeID", &a.boolType}, {"CFRelease", &a.release}, {"CFDataCreate", &a.dataCreate}, {"CFDictionaryCreate", &a.dictCreate}, {"CFStringCreateWithCString", &a.strCreate}, {"CFStringGetCString", &a.strGet}, {"CFDictionaryGetValue", &a.dictGet}, {"CFNumberGetValue", &a.numberGet}, {"CFBooleanGetValue", &a.boolGet}} {
		if !bind(cf, v.name, v.fn) {
			return ErrAccess
		}
	}
	for _, v := range []struct {
		name string
		fn   any
	}{{"SecCodeCopyGuestWithAttributes", &a.guestCopy}, {"SecRequirementCreateWithString", &a.reqCreate}, {"SecCodeCheckValidity", &a.check}, {"SecCodeCopyStaticCode", &a.staticCopy}, {"SecStaticCodeCheckValidity", &a.staticCheck}, {"SecCodeCopySigningInformation", &a.infoCopy}} {
		if !bind(sec, v.name, v.fn) {
			return ErrAccess
		}
	}
	system, e := open("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if e != nil {
		return ErrAccess
	}
	proc, e := open("/usr/lib/libproc.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if e != nil {
		return ErrAccess
	}
	if !bind(proc, "proc_pidinfo", &a.pidInfo) {
		return ErrAccess
	}
	var copyValue func(*uintptr, uintptr, uintptr) uintptr
	if !bind(system, "getsockopt", &a.sockopt) {
		return ErrAccess
	}
	if !bind(system, "memcpy", &copyValue) {
		return ErrAccess
	}
	for _, v := range []struct {
		name string
		out  *uintptr
	}{{"kSecGuestAttributeAudit", &a.audit}, {"kSecCodeInfoFlags", &a.flags}, {"kSecCodeInfoPList", &a.plist}, {"kSecCodeInfoEntitlementsDict", &a.entitlements}} {
		p, e := lookup(sec, v.name)
		if e != nil || p == 0 {
			return ErrAccess
		}
		copyValue(v.out, p, unsafe.Sizeof(uintptr(0)))
		if *v.out == 0 {
			return ErrAccess
		}
	}
	return nil
}
func auditPeer(c *net.UnixConn) (Peer, error) {
	return auditPeerUsing(c, loadSecurity)
}
func auditPeerUsing(c *net.UnixConn, load func() (*securityAPI, error)) (Peer, error) {
	return auditPeerObservedUsing(c, load, Peer{}, nil)
}
func auditPeerObservedUsing(c *net.UnixConn, load func() (*securityAPI, error), expected Peer, checks *ProofChecks) (Peer, error) {
	var p Peer
	a, e := load()
	if e != nil {
		return p, ErrAccess
	}
	raw, e := c.SyscallConn()
	if e != nil {
		return p, ErrAccess
	}
	var se error
	e = raw.Control(func(fd uintptr) {
		n := uint32(32)
		if a.sockopt(int32(fd), unix.SOL_LOCAL, unix.LOCAL_PEERTOKEN, &p.Token[0], &n) != 0 || n != 32 {
			se = ErrAccess
			return
		}
		if checks != nil {
			mergeProofCheck(&checks.TokenMatched, p == expected)
		}
		var pid uint32
		n = 4
		result := a.sockopt(int32(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID, &pid, &n)
		if checks != nil && result == 0 && n == 4 {
			mergeProofCheck(&checks.PIDAgreed, pid == p.Token[5])
		}
		if result != 0 || n != 4 || pid != p.Token[5] {
			se = ErrAccess
			return
		}
		cred, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if checks != nil && e == nil {
			mergeProofCheck(&checks.UIDAgreed, cred.Uid == p.Token[1])
		}
		if e != nil || cred.Uid != p.Token[1] {
			se = ErrAccess
		}
	})
	if e != nil || se != nil {
		return Peer{}, ErrAccess
	}
	return p, nil
}

type peerChecks struct {
	uid        func() int
	ppid       func() int
	translated func(string) (uint32, error)
	audit      func(*net.UnixConn) (Peer, error)
	load       func() (*securityAPI, error)
}

func VerifyApp(c *net.UnixConn, expected Peer) error {
	return verifyAppUsing(c, expected, peerChecks{os.Getuid, os.Getppid, unix.SysctlUint32, auditPeer, loadSecurity})
}
func VerifyAppWithProof(c *net.UnixConn, expected Peer) (ProofChecks, error) {
	var samples [2]ProofChecks
	index := 0
	audit := func(conn *net.UnixConn) (Peer, error) {
		sample := &samples[index]
		index++
		return auditPeerObservedUsing(conn, loadSecurity, expected, sample)
	}
	e := verifyAppUsing(c, expected, peerChecks{os.Getuid, os.Getppid, unix.SysctlUint32, audit, loadSecurity})
	return combineProofChecks(samples[0], samples[1]), e
}
func verifyAppUsing(c *net.UnixConn, expected Peer, ops peerChecks) error {
	if expected.Token[1] != uint32(ops.uid()) || expected.Token[5] != uint32(ops.ppid()) {
		return ErrAccess
	}
	translated, e := ops.translated("sysctl.proc_translated")
	if (e != nil && e != unix.ENOENT) || translated != 0 {
		return ErrAccess
	}
	before, e := ops.audit(c)
	if e != nil || before != expected {
		return ErrAccess
	}
	a, e := ops.load()
	if e != nil {
		return ErrAccess
	}
	var arch [2]int32
	want := nativeCPU(runtime.GOARCH)
	if a.pidInfo(int32(expected.Token[5]), 19, 0, &arch[0], 8) != 8 || arch[0] != want {
		return ErrAccess
	}
	if e = a.verify(expected); e != nil {
		return ErrAccess
	}
	after, e := ops.audit(c)
	if e != nil || after != expected || ops.ppid() != int(expected.Token[5]) {
		return ErrAccess
	}
	return nil
}
func nativeCPU(arch string) int32 {
	if arch == "amd64" {
		return 0x01000007
	}
	return 0x0100000c
}

func (a *securityAPI) string(s string) uintptr {
	b := append([]byte(s), 0)
	return a.strCreate(0, &b[0], 0x08000100)
}
func (a *securityAPI) lookup(dict uintptr, key string) uintptr {
	k := a.string(key)
	if k == 0 {
		return 0
	}
	defer a.release(k)
	return a.dictGet(dict, k)
}
func (a *securityAPI) verify(peer Peer) error {
	data := a.dataCreate(0, (*byte)(unsafe.Pointer(&peer.Token[0])), 32)
	if data == 0 {
		return ErrAccess
	}
	defer a.release(data)
	key, value := a.audit, data
	dict := a.dictCreate(0, &key, &value, 1, 0, 0)
	if dict == 0 {
		return ErrAccess
	}
	defer a.release(dict)
	var guest uintptr
	if a.guestCopy(0, dict, 0, &guest) != 0 || guest == 0 {
		return ErrAccess
	}
	defer a.release(guest)
	text := a.string(`anchor apple generic and certificate leaf[field.1.2.840.113635.100.6.1.13] exists and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[subject.OU] = "S8LB56P782" and identifier "ai.sliccy.mac"`)
	if text == 0 {
		return ErrAccess
	}
	defer a.release(text)
	var req uintptr
	if a.reqCreate(text, 0, &req) != 0 || req == 0 {
		return ErrAccess
	}
	defer a.release(req)
	if a.check(guest, 0, req) != 0 {
		return ErrAccess
	}
	var static uintptr
	if a.staticCopy(guest, 0, &static) != 0 || static == 0 {
		return ErrAccess
	}
	defer a.release(static)
	if a.staticCheck(static, 16, req) != 0 {
		return ErrAccess
	}
	var info uintptr
	if a.infoCopy(guest, 2, &info) != 0 || info == 0 {
		return ErrAccess
	}
	defer a.release(info)
	if a.checkMetadata(info) != nil {
		return ErrAccess
	}
	if a.check(guest, 0, req) != 0 || a.staticCheck(static, 16, req) != 0 {
		return ErrAccess
	}
	return nil
}

type bootstrapOps struct {
	stat        func(int, *unix.Stat_t) error
	flags       func(uintptr, int, int) (int, error)
	closeOnExec func(int)
	nonblock    func(int, bool) error
	file        func(uintptr, string) *os.File
}

func bootstrapFile() (*os.File, error) {
	return bootstrapFileUsing(3, bootstrapOps{unix.Fstat, unix.FcntlInt, unix.CloseOnExec, unix.SetNonblock, os.NewFile})
}
func bootstrapFileUsing(fd int, ops bootstrapOps) (*os.File, error) {
	var st unix.Stat_t
	if ops.stat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFIFO {
		return nil, ErrAccess
	}
	flags, e := ops.flags(uintptr(fd), unix.F_GETFL, 0)
	if e != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		return nil, ErrAccess
	}
	ops.closeOnExec(fd)
	if ops.nonblock(fd, true) != nil {
		return nil, ErrAccess
	}
	return ops.file(uintptr(fd), "native-screen-bootstrap"), nil
}

type peerMonitorOps struct {
	queue   func() (int, error)
	event   func(int, []unix.Kevent_t, []unix.Kevent_t, *unix.Timespec) (int, error)
	close   func(int) error
	cloexec func(int)
}

func monitorPeer(c *Client) error {
	return monitorPeerUsing(c, peerMonitorOps{unix.Kqueue, unix.Kevent, unix.Close, unix.CloseOnExec})
}
func monitorPeerUsing(c *Client, ops peerMonitorOps) error {
	kq, e := ops.queue()
	if e != nil {
		return ErrAccess
	}
	ops.cloexec(kq)
	change := unix.Kevent_t{Ident: uint64(c.peer.Token[5]), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ENABLE | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT | unix.NOTE_EXEC}
	if _, e = ops.event(kq, []unix.Kevent_t{change}, nil, nil); e != nil {
		_ = ops.close(kq)
		return ErrAccess
	}
	if c.verifyPeer(c.safety, "safety") != nil {
		_ = ops.close(kq)
		return ErrAccess
	}
	go func() {
		defer func() { _ = ops.close(kq) }()
		for {
			select {
			case <-c.stop:
				return
			default:
			}
			var events [1]unix.Kevent_t
			timeout := unix.Timespec{Nsec: 250000000}
			n, e := ops.event(kq, nil, events[:], &timeout)
			if e != nil && e != unix.EINTR {
				c.Close()
				return
			}
			if n > 0 {
				c.Close()
				return
			}
		}
	}()
	return nil
}

func (a *securityAPI) checkMetadata(info uintptr) error {
	var flags int64
	f := a.dictGet(info, a.flags)
	if f == 0 || a.typeID(f) != a.numberType() || !a.numberGet(f, 4, &flags) || flags&0x10000 == 0 || flags&4 != 0 {
		return ErrAccess
	}
	if ent := a.dictGet(info, a.entitlements); ent != 0 {
		if a.typeID(ent) != a.dictType() {
			return ErrAccess
		}
		for _, key := range []string{"get-task-allow", "com.apple.security.get-task-allow", "com.apple.security.cs.allow-jit", "com.apple.security.cs.disable-library-validation", "com.apple.security.cs.allow-dyld-environment-variables", "com.apple.security.cs.allow-unsigned-executable-memory"} {
			if v := a.lookup(ent, key); v != 0 {
				if a.typeID(v) != a.boolType() || a.boolGet(v) {
					return ErrAccess
				}
			}
		}
	}
	plist := a.dictGet(info, a.plist)
	if plist == 0 || a.typeID(plist) != a.dictType() {
		return ErrAccess
	}
	v := a.lookup(plist, "CFBundleShortVersionString")
	if v == 0 || a.typeID(v) != a.stringType() {
		return ErrAccess
	}
	var version [65]byte
	if !a.strGet(v, &version[0], 65, 0x08000100) {
		return ErrAccess
	}
	n := 0
	for n < len(version) && version[n] != 0 {
		n++
	}
	if n == len(version) || !CompareVersion(string(version[:n]), MinimumAppVersion) {
		return ErrAccess
	}
	return nil
}

func mergeProofCheck(target **bool, matched bool) {
	if *target != nil {
		matched = matched && **target
	}
	value := matched
	*target = &value
}

func combineProofChecks(before, after ProofChecks) ProofChecks {
	return ProofChecks{combineProofValue(before.TokenMatched, after.TokenMatched), combineProofValue(before.PIDAgreed, after.PIDAgreed), combineProofValue(before.UIDAgreed, after.UIDAgreed)}
}
func combineProofValue(before, after *bool) *bool {
	if (before != nil && !*before) || (after != nil && !*after) {
		value := false
		return &value
	}
	if before == nil || after == nil {
		return nil
	}
	value := true
	return &value
}
