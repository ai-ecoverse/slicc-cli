package udp

import (
	"bufio"
	"container/list"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/tcp"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

const (
	readBuffer    = 128 << 10
	dropReportGap = time.Second
)

type session struct {
	s      *link.Stream
	c      *net.UDPConn
	o      Options
	family int
	name   string

	mu    sync.Mutex
	peers map[netip.AddrPort]*list.Element
	order *list.List

	last      atomic.Int64
	closed    atomic.Bool
	closeOnce sync.Once
	idle      *time.Timer

	dropMu    sync.Mutex
	pending   int
	dropped   int
	dropTimer *time.Timer

	sent, received atomic.Int64
}

func newSession(s *link.Stream, c *net.UDPConn, o Options, family int, name string) *session {
	return &session{s: s, c: c, o: o, family: family, name: name, peers: map[netip.AddrPort]*list.Element{}, order: list.New()}
}

func (x *session) run() {
	x.touch()
	x.idle = time.AfterFunc(x.o.Idle, x.checkIdle)
	go func() {
		<-x.s.Done()
		x.shut(false)
	}()
	downDone := make(chan struct{})
	go func() {
		defer close(downDone)
		x.down()
	}()
	x.up()
	<-downDone
	x.idle.Stop()
	dropped := x.flushDrops(true)
	x.o.Logf("udp %s: closed, %d sent, %d received, %d dropped", x.name, x.sent.Load(), x.received.Load(), dropped)
}

func (x *session) touch() { x.last.Store(time.Now().UnixNano()) }

func (x *session) checkIdle() {
	if x.closed.Load() {
		return
	}
	left := x.o.Idle - time.Since(time.Unix(0, x.last.Load()))
	if left > 0 {
		x.idle.Reset(left)
		return
	}
	x.o.Logf("udp %s: idle, closing", x.name)
	x.shut(true)
}

func (x *session) shut(end bool) {
	x.closeOnce.Do(func() {
		x.closed.Store(true)
		_ = x.c.Close()
	})
	if end {
		_ = x.s.CloseWrite()
	}
}

func (x *session) up() {
	r := bufio.NewReaderSize(x.s, readBuffer)
	buf := make([]byte, MaxPayload6)
	for !x.closed.Load() {
		d, err := readRecord(r, x.family, buf)
		var pe protoError
		switch {
		case errors.As(err, &pe):
			x.o.Logf("udp %s: EPROTO, %s", x.name, pe)
			_ = x.s.Reset("EPROTO: " + pe.Error())
			x.shut(false)
			return
		case err != nil:
			x.shut(err == io.EOF)
			return
		}
		if x.closed.Load() {
			break
		}
		x.send(d)
	}
	_, _ = io.Copy(io.Discard, r)
}

func (x *session) send(d datagram) {
	x.touch()
	if err := x.o.Policy.CheckDatagram(d.to.Addr(), d.to.Port()); err != nil {
		x.o.Logf("udp %s: %s refused", x.name, d.to)
		x.unreachable(d.to, policy.Errno+": "+err.Error())
		return
	}
	fresh := x.remember(d.to)
	_, err := x.c.WriteToUDPAddrPort(d.payload, d.to)
	if err != nil && x.drainErrors() > 0 {
		_, err = x.c.WriteToUDPAddrPort(d.payload, d.to)
	}
	if err != nil {
		if fresh {
			x.forget(d.to)
		}
		if x.closed.Load() {
			return
		}
		code := sendErrno(err)
		x.o.Logf("udp %s: %s: %s", x.name, d.to, code)
		x.unreachable(d.to, code+": "+syscallText(err))
		return
	}
	x.sent.Add(1)
	x.used(d.to)
	if fresh {
		x.o.Logf("udp %s: to %s", x.name, d.to)
	}
}

func (x *session) remember(to netip.AddrPort) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	if _, ok := x.peers[to]; ok {
		return false
	}
	x.peers[to] = x.order.PushBack(to)
	return true
}

func (x *session) used(to netip.AddrPort) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if e, ok := x.peers[to]; ok {
		x.order.MoveToBack(e)
	}
	for x.order.Len() > MaxPeers {
		oldest := x.order.Front()
		delete(x.peers, oldest.Value.(netip.AddrPort))
		x.order.Remove(oldest)
	}
}

func (x *session) forget(to netip.AddrPort) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if e, ok := x.peers[to]; ok {
		x.order.Remove(e)
		delete(x.peers, to)
	}
}

func (x *session) known(from netip.AddrPort) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	_, ok := x.peers[from]
	return ok
}

func (x *session) down() {
	buf := make([]byte, 1<<16)
	for {
		n, from, err := x.c.ReadFromUDPAddrPort(buf)
		if err != nil {
			if x.closed.Load() || errors.Is(err, net.ErrClosed) {
				return
			}
			if x.drainErrors() > 0 || transient(err) {
				continue
			}
			x.o.Logf("udp %s: ECONNRESET: %v", x.name, err)
			_ = x.s.Reset("ECONNRESET: " + err.Error())
			x.shut(false)
			return
		}
		from = netip.AddrPortFrom(from.Addr().Unmap().WithZone(""), from.Port())
		if !x.known(from) {
			continue
		}
		x.touch()
		x.received.Add(1)
		if !x.deliver(Record(RecordDatagram, from.Addr().String(), from.Port(), buf[:n])) {
			return
		}
	}
}

func (x *session) unreachable(to netip.AddrPort, reason string) {
	x.deliver(Record(RecordUnreachable, to.Addr().String(), to.Port(), []byte(reason)))
}

func (x *session) deliver(rec []byte) bool {
	ok, err := x.s.TryWrite(rec)
	if err != nil {
		x.shut(false)
		return false
	}
	if !ok {
		x.drop()
	}
	return true
}

func (x *session) drop() {
	x.dropMu.Lock()
	defer x.dropMu.Unlock()
	x.pending++
	if x.dropTimer == nil {
		x.dropTimer = time.AfterFunc(dropReportGap, func() { x.flushDrops(false) })
	}
}

func (x *session) flushDrops(final bool) int {
	x.dropMu.Lock()
	n := x.pending
	x.dropped += n
	x.pending = 0
	if x.dropTimer != nil && final {
		x.dropTimer.Stop()
	}
	x.dropTimer = nil
	total := x.dropped
	x.dropMu.Unlock()
	if n > 0 {
		x.o.Logf("udp %s: dropped %d datagrams, the page's window was full (%d so far)", x.name, n, total)
		x.o.Dropped(x.s, n)
	}
	return total
}

func sendErrno(err error) string {
	var se syscall.Errno
	if errors.As(err, &se) && (se == syscall.EMSGSIZE || se == 10040) {
		return "EMSGSIZE"
	}
	return tcp.Errno(err)
}

func syscallText(err error) string {
	var se syscall.Errno
	if errors.As(err, &se) {
		return se.Error()
	}
	return err.Error()
}

func transient(err error) bool {
	var se syscall.Errno
	if !errors.As(err, &se) {
		return false
	}
	if se == syscall.ECONNREFUSED || se == 10061 || se == syscall.EMSGSIZE || se == 10040 {
		return true
	}
	switch tcp.Errno(se) {
	case "ECONNRESET", "ENETUNREACH", "EHOSTUNREACH":
		return true
	}
	return false
}
