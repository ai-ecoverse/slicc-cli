package integration

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/udp"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

const udpSecretPayload = "udp-payload-0451"

type udpRec struct {
	Type   int    `json:"type"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
	Text   string `json:"text"`
}

type udpState struct {
	Records []udpRec `json:"records"`
	Ended   bool     `json:"ended"`
	Reset   string   `json:"reset"`
	Detail  string   `json:"detail"`
	Error   string   `json:"error"`
	Done    bool     `json:"done"`
}

type udpSession struct {
	e     *linkEnv
	id    int
	local netip.AddrPort
}

func udpEcho(t *testing.T, network, addr string) netip.AddrPort {
	t.Helper()
	c, ok := udpListen(t, network, addr)
	if !ok {
		t.Fatalf("listen %s %s failed", network, addr)
	}
	go func() {
		buf := make([]byte, 1<<16)
		for {
			n, from, err := c.ReadFromUDPAddrPort(buf)
			if err != nil {
				return
			}
			_, _ = c.WriteToUDPAddrPort(buf[:n], from)
		}
	}()
	return netip.MustParseAddrPort(c.LocalAddr().String())
}

func udpListen(t *testing.T, network, addr string) (*net.UDPConn, bool) {
	t.Helper()
	a, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return nil, false
	}
	c, err := net.ListenUDP(network, a)
	if err != nil {
		return nil, false
	}
	_ = c.SetReadBuffer(1 << 20)
	_ = c.SetWriteBuffer(1 << 20)
	t.Cleanup(func() { _ = c.Close() })
	return c, true
}

func udpHasIPv6(t *testing.T) bool {
	t.Helper()
	c, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func (e *linkEnv) udpOpen(t *testing.T, open map[string]any, paused bool) (*udpSession, map[string]any) {
	t.Helper()
	var r map[string]any
	e.p.eval("linkTest.udp.open("+jsArgs(open, map[string]any{"paused": paused})+")", &r)
	id, ok := r["id"].(float64)
	if !ok {
		return nil, r
	}
	opened, _ := r["opened"].(map[string]any)
	local, _ := opened["localAddr"].(string)
	ap, err := netip.ParseAddrPort(local)
	if err != nil || ap.Port() == 0 || !ap.Addr().IsUnspecified() {
		t.Fatalf("OPENED %v", r)
	}
	return &udpSession{e: e, id: int(id), local: ap}, r
}

func (e *linkEnv) udpSocket(t *testing.T, family int) *udpSession {
	t.Helper()
	s, r := e.udpOpen(t, map[string]any{"family": family}, false)
	if s == nil {
		t.Fatalf("udp OPEN family %d: %v", family, r)
	}
	return s
}

func udpTo(ap netip.AddrPort, text string) map[string]any {
	return map[string]any{"host": ap.Addr().String(), "port": ap.Port(), "text": text}
}

func (s *udpSession) send(t *testing.T, datagrams ...map[string]any) {
	t.Helper()
	var r map[string]any
	s.e.p.eval("linkTest.udp.send("+jsArgs(s.id, datagrams)+")", &r)
	if r["ok"] != true {
		t.Fatalf("send: %v", r)
	}
}

func (s *udpSession) wait(t *testing.T, n int) udpState {
	t.Helper()
	var st udpState
	s.e.p.eval("linkTest.udp.wait("+jsArgs(s.id, n, 10000)+")", &st)
	if len(st.Records) < n {
		t.Fatalf("wanted %d records: %+v", n, st)
	}
	return st
}

func (s *udpSession) waitEnd(t *testing.T) udpState {
	t.Helper()
	var st udpState
	s.e.p.eval("linkTest.udp.waitEnd("+jsArgs(s.id, 10000)+")", &st)
	return st
}

func (s *udpSession) end(t *testing.T) udpState {
	t.Helper()
	var st udpState
	s.e.p.eval("linkTest.udp.end("+jsArgs(s.id, 10000)+")", &st)
	return st
}

func (s *udpSession) port() string { return strconv.Itoa(int(s.local.Port())) }

func udpPattern(size, seed int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte((i*7 + seed) % 251)
	}
	return out
}

func udpRaw(t byte, host string, port uint16, length uint32, payload []byte) string {
	b := []byte{t, byte(len(host) >> 8), byte(len(host))}
	b = append(b, host...)
	b = append(b, byte(port>>8), byte(port), byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
	b = append(b, payload...)
	return base64.StdEncoding.EncodeToString(b)
}

func TestLinkUDP(t *testing.T) {
	env := setupLink(t, false)
	echo := udpEcho(t, "udp4", "127.0.0.1:0")
	t.Run("hello caps", func(t *testing.T) {
		var h struct {
			Caps []string `json:"caps"`
		}
		env.p.eval("linkTest.ready()", &h)
		if !slices.Contains(h.Caps, "udp") {
			t.Fatalf("caps %v lack udp", h.Caps)
		}
	})
	t.Run("round trip over IPv4", func(t *testing.T) { env.testUDPRoundTrip(t, 4, echo) })
	t.Run("round trip over IPv6", func(t *testing.T) {
		if !udpHasIPv6(t) {
			t.Skip("no IPv6 loopback on this runner")
		}
		env.testUDPRoundTrip(t, 6, udpEcho(t, "udp6", "[::1]:0"))
	})
	t.Run("several destinations", func(t *testing.T) { env.testUDPSeveral(t, echo) })
	t.Run("a refused destination", func(t *testing.T) { env.testUDPRefused(t, echo) })
	t.Run("broadcast and multicast", func(t *testing.T) { env.testUDPBroadcast(t, echo) })
	t.Run("unsolicited sources", func(t *testing.T) { env.testUDPUnsolicited(t, echo) })
	t.Run("unreachable ports", func(t *testing.T) { env.testUDPUnreachable(t, echo) })
	t.Run("malformed records", func(t *testing.T) { env.testUDPMalformed(t, echo) })
	t.Run("datagram size", func(t *testing.T) { env.testUDPSize(t, echo) })
	t.Run("a full window drops", func(t *testing.T) { env.testUDPWindow(t, echo) })
	t.Run("END closes the socket", func(t *testing.T) { env.testUDPEnd(t, echo) })
	t.Run("nothing private is logged", func(t *testing.T) { env.testUDPPrivacy(t, echo) })
}

func (e *linkEnv) testUDPRoundTrip(t *testing.T, family int, echo netip.AddrPort) {
	s := e.udpSocket(t, family)
	if family == 6 != s.local.Addr().Is6() {
		t.Fatalf("family %d bound %s", family, s.local)
	}
	s.send(t, udpTo(echo, "ping"), udpTo(echo, ""))
	st := s.wait(t, 2)
	want := []udpRec{
		{Type: 1, Host: echo.Addr().String(), Port: int(echo.Port()), Bytes: 4, Text: "ping"},
		{Type: 1, Host: echo.Addr().String(), Port: int(echo.Port()), Bytes: 0, Text: ""},
	}
	for i, r := range st.Records {
		r.SHA256 = ""
		if r != want[i] {
			t.Fatalf("record %d: %+v, want %+v", i, r, want[i])
		}
	}
	if st := s.end(t); !st.Ended || !st.Done || st.Reset != "" {
		t.Fatalf("end: %+v", st)
	}
}

func (e *linkEnv) testUDPSeveral(t *testing.T, echo netip.AddrPort) {
	other := udpEcho(t, "udp4", "127.0.0.1:0")
	s := e.udpSocket(t, 4)
	s.send(t, udpTo(echo, "one"), udpTo(other, "two"), udpTo(echo, "three"))
	st := s.wait(t, 3)
	got := map[string]int{}
	for _, r := range st.Records {
		got[r.Text] = r.Port
	}
	if got["one"] != int(echo.Port()) || got["two"] != int(other.Port()) || got["three"] != int(echo.Port()) {
		t.Fatalf("sources %+v", st.Records)
	}
	s.end(t)
}

func (e *linkEnv) testUDPRefused(t *testing.T, echo netip.AddrPort) {
	s := e.udpSocket(t, 4)
	s.send(t, map[string]any{"host": "127.0.0.1", "port": e.b.port, "text": "to the debugging port"})
	st := s.wait(t, 1)
	r := st.Records[0]
	if r.Type != 2 || r.Host != "127.0.0.1" || r.Port != e.b.port || !strings.HasPrefix(r.Text, "EACCES: ") {
		t.Fatalf("refusal %+v", st)
	}
	s.send(t, udpTo(echo, "still open"))
	st = s.wait(t, 2)
	if st.Records[1].Type != 1 || st.Records[1].Text != "still open" || st.Reset != "" {
		t.Fatalf("after the refusal %+v", st)
	}
	s.end(t)
}

func (e *linkEnv) testUDPBroadcast(t *testing.T, echo netip.AddrPort) {
	dests := []string{"255.255.255.255", "224.0.0.251", "239.255.255.250", "0.0.0.0"}
	if b, ok := directedBroadcast(); ok {
		dests = append(dests, b)
	}
	s := e.udpSocket(t, 4)
	for _, d := range dests {
		s.send(t, map[string]any{"host": d, "port": 5353, "text": "everyone"})
	}
	st := s.wait(t, len(dests))
	for i, r := range st.Records {
		if r.Type != 2 || r.Host != dests[i] || r.Port != 5353 || !strings.HasPrefix(r.Text, "EACCES: ") {
			t.Fatalf("record %d: %+v", i, r)
		}
	}
	s.send(t, udpTo(echo, "unicast"))
	if st := s.wait(t, len(dests)+1); st.Records[len(dests)].Text != "unicast" {
		t.Fatalf("after the refusals %+v", st)
	}
	s.end(t)
	if !udpHasIPv6(t) {
		return
	}
	s6 := e.udpSocket(t, 6)
	s6.send(t, map[string]any{"host": "ff02::fb", "port": 5353, "text": "everyone"}, map[string]any{"host": "::", "port": 5353, "text": "nobody"})
	st = s6.wait(t, 2)
	for _, r := range st.Records {
		if r.Type != 2 || !strings.HasPrefix(r.Text, "EACCES: ") {
			t.Fatalf("v6 %+v", st)
		}
	}
	s6.end(t)
}

func directedBroadcast() (string, bool) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", false
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		ip4 := n.IP.To4()
		if !ok || ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
			continue
		}
		mask := n.Mask
		if len(mask) == net.IPv6len {
			mask = mask[12:]
		}
		if ones, _ := mask.Size(); ones >= 31 || ones == 0 {
			continue
		}
		b := make(net.IP, 4)
		for i := range b {
			b[i] = ip4[i] | ^mask[i]
		}
		return b.String(), true
	}
	return "", false
}

func (e *linkEnv) testUDPUnsolicited(t *testing.T, echo netip.AddrPort) {
	stray, _ := udpListen(t, "udp4", "127.0.0.1:0")
	s := e.udpSocket(t, 4)
	s.send(t, udpTo(echo, "first"))
	s.wait(t, 1)
	to := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), s.local.Port())
	for i := 0; i < 3; i++ {
		if _, err := stray.WriteToUDPAddrPort([]byte("unsolicited"), to); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	s.send(t, udpTo(echo, "second"))
	s.wait(t, 2)
	time.Sleep(300 * time.Millisecond)
	st := s.wait(t, 2)
	if len(st.Records) != 2 || st.Records[0].Text != "first" || st.Records[1].Text != "second" {
		t.Fatalf("records %+v", st.Records)
	}
	s.end(t)
}

func (e *linkEnv) testUDPUnreachable(t *testing.T, echo netip.AddrPort) {
	gone, _ := udpListen(t, "udp4", "127.0.0.1:0")
	closed := netip.MustParseAddrPort(gone.LocalAddr().String())
	_ = gone.Close()
	s := e.udpSocket(t, 4)
	s.send(t, udpTo(closed, "anyone there"))
	n := 0
	if runtime.GOOS == "linux" {
		st := s.wait(t, 1)
		r := st.Records[0]
		if r.Type != 2 || r.Host != "127.0.0.1" || r.Port != int(closed.Port()) || !strings.HasPrefix(r.Text, "ECONNREFUSED") {
			t.Fatalf("unreachable %+v", st)
		}
		n = 1
	}
	time.Sleep(200 * time.Millisecond)
	s.send(t, udpTo(echo, "still open"))
	st := s.wait(t, n+1)
	if st.Reset != "" || len(st.Records) != n+1 || st.Records[n].Text != "still open" {
		t.Fatalf("after an unreachable port %+v", st)
	}
	s.end(t)
}

func (e *linkEnv) testUDPMalformed(t *testing.T, echo netip.AddrPort) {
	type bad struct {
		family int
		raw    string
		end    bool
	}
	port := echo.Port()
	x := []byte("x")
	cases := []bad{
		{4, udpRaw(3, "127.0.0.1", port, 1, x), false},
		{4, udpRaw(2, "127.0.0.1", port, 1, x), false},
		{4, udpRaw(1, "example.com", port, 1, x), false},
		{4, udpRaw(1, "", port, 1, x), false},
		{4, udpRaw(1, "::1", port, 1, x), false},
		{4, udpRaw(1, "127.0.0.1", 0, 1, x), false},
		{4, udpRaw(1, strings.Repeat("1", 65), port, 1, x), false},
		{4, udpRaw(1, "127.0.0.1", port, 5, x), true},
	}
	if udpHasIPv6(t) {
		cases = append(cases,
			bad{6, udpRaw(1, "127.0.0.1", port, 1, x), false},
			bad{6, udpRaw(1, "::ffff:127.0.0.1", port, 1, x), false},
			bad{6, udpRaw(1, "fe80::1%eth0", port, 1, x), false},
		)
	}
	for i, c := range cases {
		s := e.udpSocket(t, c.family)
		s.send(t, map[string]any{"raw": c.raw})
		if c.end {
			s.end(t)
		}
		st := s.waitEnd(t)
		if st.Reset != "EPROTO" {
			t.Fatalf("case %d (family %d): %+v", i, c.family, st)
		}
	}
	for _, open := range []map[string]any{{"family": 5}, {}, {"family": "4"}, {"family": 0}} {
		s, r := e.udpOpen(t, open, false)
		if s != nil || r["reset"] != "EPROTO" {
			t.Fatalf("OPEN %v: %v", open, r)
		}
	}
}

func (e *linkEnv) testUDPSize(t *testing.T, echo netip.AddrPort) {
	s := e.udpSocket(t, 4)
	s.send(t, map[string]any{"host": echo.Addr().String(), "port": echo.Port(), "size": udp.MaxPayload4, "seed": 3})
	st := s.wait(t, 1)
	sum := sha256.Sum256(udpPattern(udp.MaxPayload4, 3))
	if r := st.Records[0]; r.Type != 1 || r.Bytes != udp.MaxPayload4 || r.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("max datagram %+v", r)
	}
	s.end(t)
	over := e.udpSocket(t, 4)
	over.send(t, map[string]any{"raw": udpRaw(1, echo.Addr().String(), echo.Port(), udp.MaxPayload4+1, nil)})
	if st := over.waitEnd(t); st.Reset != "EPROTO" || !strings.Contains(st.Detail, "65507") {
		t.Fatalf("oversize v4 %+v", st)
	}
	if !udpHasIPv6(t) {
		return
	}
	over6 := e.udpSocket(t, 6)
	over6.send(t, map[string]any{"raw": udpRaw(1, "::1", echo.Port(), udp.MaxPayload6+1, nil)})
	if st := over6.waitEnd(t); st.Reset != "EPROTO" || !strings.Contains(st.Detail, "65527") {
		t.Fatalf("oversize v6 %+v", st)
	}
}

func (e *linkEnv) testUDPWindow(t *testing.T, echo netip.AddrPort) {
	s, r := e.udpOpen(t, map[string]any{"family": 4}, true)
	if s == nil {
		t.Fatalf("open %v", r)
	}
	const n, size = 12, 60000
	var ds []map[string]any
	for i := 0; i < n; i++ {
		ds = append(ds, map[string]any{"host": echo.Addr().String(), "port": echo.Port(), "size": size, "seed": i, "pause": 20})
	}
	s.send(t, ds...)
	e.a.waitStderr(t, "udp "+s.local.String()+": dropped ", 15*time.Second)
	var ok map[string]any
	e.p.eval("linkTest.udp.resume("+jsArgs(s.id)+")", &ok)
	time.Sleep(500 * time.Millisecond)
	st := s.wait(t, 1)
	if len(st.Records) == 0 || len(st.Records) >= n || st.Reset != "" {
		t.Fatalf("%d of %d datagrams arrived: %+v", len(st.Records), n, st.Reset)
	}
	s.send(t, udpTo(echo, "after"))
	st = s.wait(t, len(st.Records)+1)
	if st.Records[len(st.Records)-1].Text != "after" {
		t.Fatalf("after the drops %+v", st.Records[len(st.Records)-1])
	}
	s.end(t)
	e.a.waitStderr(t, fmt.Sprintf("udp %s: closed, %d sent, ", s.local, n+1), 10*time.Second)
}

func (e *linkEnv) testUDPEnd(t *testing.T, echo netip.AddrPort) {
	s := e.udpSocket(t, 4)
	s.send(t, udpTo(echo, "bye"))
	s.wait(t, 1)
	if st := s.end(t); !st.Ended || !st.Done || st.Reset != "" {
		t.Fatalf("end %+v", st)
	}
	e.a.waitStderr(t, "udp "+s.local.String()+": closed", 10*time.Second)
	c, ok := udpListen(t, "udp4", "0.0.0.0:"+s.port())
	if !ok {
		t.Fatalf("port %s is still bound after END", s.port())
	}
	_ = c.Close()
}

func (e *linkEnv) testUDPPrivacy(t *testing.T, echo netip.AddrPort) {
	s := e.udpSocket(t, 4)
	s.send(t, udpTo(echo, udpSecretPayload), map[string]any{"host": "224.0.0.1", "port": 9, "text": udpSecretPayload})
	s.wait(t, 2)
	s.end(t)
	e.a.waitStderr(t, "udp "+s.local.String()+": closed", 10*time.Second)
	logs := e.a.stderr.String()
	if strings.Contains(logs, udpSecretPayload) {
		t.Fatalf("the node log shows the payload:\n%s", logs)
	}
	if !strings.Contains(logs, "udp "+s.local.String()+": to "+echo.String()) {
		t.Fatalf("no destination line in the log:\n%s", logs)
	}
}

func TestLinkUDPIdleTimeout(t *testing.T) {
	const idle = 800 * time.Millisecond
	env := setupInProcess(t, policy.Local(), func(n *node.Node) {
		udp.RegisterOptions(n, udp.Options{Policy: n.Policy(), Logf: n.Logf, Idle: idle, Dropped: n.Dropped})
	})
	var h struct {
		Caps []string `json:"caps"`
	}
	env.p.eval("linkTest.ready()", &h)
	if !slices.Equal(h.Caps, []string{"udp"}) {
		t.Fatalf("caps %v", h.Caps)
	}
	echo := udpEcho(t, "udp4", "127.0.0.1:0")
	s := env.udpSocket(t, 4)
	for i := 0; i < 5; i++ {
		s.send(t, udpTo(echo, "keepalive"))
		time.Sleep(idle / 3)
	}
	st := s.wait(t, 5)
	if st.Ended {
		t.Fatalf("closed while active: %+v", st)
	}
	start := time.Now()
	st = s.waitEnd(t)
	if !st.Ended || st.Reset != "" {
		t.Fatalf("idle socket not closed: %+v", st)
	}
	if took := time.Since(start); took < idle/2 {
		t.Fatalf("closed after %v, before the idle timeout", took)
	}
	if st := s.end(t); !st.Done {
		t.Fatalf("end after idle %+v", st)
	}
	env.a.waitStderr(t, "udp "+s.local.String()+": idle, closing", 5*time.Second)
	c, ok := udpListen(t, "udp4", "0.0.0.0:"+s.port())
	if !ok {
		t.Fatalf("port %s is still bound after the idle timeout", s.port())
	}
	_ = c.Close()
}
