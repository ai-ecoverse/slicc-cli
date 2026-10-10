package integration

import (
	"context"
	wire "encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

type fakeDNS struct {
	mu   sync.Mutex
	zone map[string][]netip.Addr
	conn net.PacketConn
	addr string
}

func newFakeDNS(t *testing.T) *fakeDNS {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDNS{zone: map[string][]netip.Addr{}, conn: pc, addr: pc.LocalAddr().String()}
	t.Cleanup(func() { _ = pc.Close() })
	go d.serve()
	return d
}

func (d *fakeDNS) set(name string, addrs ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	list := make([]netip.Addr, len(addrs))
	for i, a := range addrs {
		list[i] = netip.MustParseAddr(a)
	}
	d.zone[strings.ToLower(strings.TrimSuffix(name, "."))] = list
}

func (d *fakeDNS) serve() {
	buf := make([]byte, 1500)
	for {
		n, from, err := d.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if resp := d.answer(buf[:n]); resp != nil {
			_, _ = d.conn.WriteTo(resp, from)
		}
	}
}

func (d *fakeDNS) answer(q []byte) []byte {
	if len(q) < 12 {
		return nil
	}
	i := 12
	var labels []string
	for i < len(q) && q[i] != 0 {
		l := int(q[i])
		if i+1+l > len(q) {
			return nil
		}
		labels = append(labels, string(q[i+1:i+1+l]))
		i += 1 + l
	}
	if i+5 > len(q) {
		return nil
	}
	question := q[12 : i+5]
	qtype := wire.BigEndian.Uint16(q[i+1:])
	d.mu.Lock()
	addrs, known := d.zone[strings.ToLower(strings.Join(labels, "."))]
	d.mu.Unlock()
	var answers [][]byte
	for _, a := range addrs {
		if (qtype == 1 && a.Is4()) || (qtype == 28 && a.Is6()) {
			answers = append(answers, a.AsSlice())
		}
	}
	flags := uint16(0x8580)
	if !known {
		flags |= 3
	}
	resp := wire.BigEndian.AppendUint16(nil, wire.BigEndian.Uint16(q))
	resp = wire.BigEndian.AppendUint16(resp, flags)
	resp = wire.BigEndian.AppendUint16(resp, 1)
	resp = wire.BigEndian.AppendUint16(resp, uint16(len(answers)))
	resp = wire.BigEndian.AppendUint32(resp, 0)
	resp = append(resp, question...)
	for _, rdata := range answers {
		resp = append(resp, 0xc0, 0x0c)
		resp = wire.BigEndian.AppendUint16(resp, qtype)
		resp = wire.BigEndian.AppendUint16(resp, 1)
		resp = wire.BigEndian.AppendUint32(resp, 30)
		resp = wire.BigEndian.AppendUint16(resp, uint16(len(rdata)))
		resp = append(resp, rdata...)
	}
	return resp
}

func (d *fakeDNS) resolver() *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var nd net.Dialer
			return nd.DialContext(ctx, "udp4", d.addr)
		},
	}
}

func (d *fakeDNS) dialer(p *policy.Policy) *policy.Dialer {
	pd := policy.NewDialer(p)
	pd.Base.Resolver = d.resolver()
	pd.Base.Timeout = 10 * time.Second
	return pd
}

type slot struct {
	data string
	done bool
}

type listener struct {
	net.Listener
	mu       sync.Mutex
	accepted []*slot
}

func listen(t *testing.T, address string) *listener {
	t.Helper()
	ln, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	return serve(t, ln)
}

func serve(t *testing.T, ln net.Listener) *listener {
	l := &listener{Listener: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s := &slot{}
			l.mu.Lock()
			l.accepted = append(l.accepted, s)
			l.mu.Unlock()
			go l.handle(c, s)
		}
	}()
	return l
}

func (l *listener) handle(c net.Conn, s *slot) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = c.Write([]byte(l.Addr().String()))
	buf := make([]byte, 8)
	n, _ := io.ReadFull(c, buf)
	l.mu.Lock()
	s.data, s.done = string(buf[:n]), true
	l.mu.Unlock()
}

func (l *listener) port() int { return l.Addr().(*net.TCPAddr).Port }

func (l *listener) assertNoLeak(t *testing.T) {
	t.Helper()
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, _ = c.Write([]byte("sentinel"))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if leaked, ok := l.beforeSentinel(); ok {
			if len(leaked) > 0 {
				t.Fatalf("%s was reached through the policy: %q", l.Addr(), leaked)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("sentinel never arrived at %s", l.Addr())
}

func (l *listener) beforeSentinel() ([]string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var leaked []string
	for _, s := range l.accepted {
		if !s.done {
			return nil, false
		}
		if s.data == "sentinel" {
			return leaked, true
		}
		if s.data != "greeting" {
			leaked = append(leaked, s.data)
		}
	}
	return nil, false
}

func dialAndGreet(t *testing.T, d *policy.Dialer, address string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return "", err
	}
	defer func() { _ = c.Close() }()
	_, _ = c.Write([]byte("greeting"))
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.RemoteAddr().String(); got != string(buf[:n]) {
		t.Fatalf("connected to %s, served by %s", got, buf[:n])
	}
	return string(buf[:n]), nil
}

func requireDenied(t *testing.T, err error) {
	t.Helper()
	var pe *policy.Error
	if !errors.Is(err, policy.ErrDenied) || !errors.As(err, &pe) || pe.Errno() != "EACCES" {
		t.Fatalf("want a policy refusal, got %v", err)
	}
}

func dualLoopback(t *testing.T) (*listener, *listener) {
	t.Helper()
	for range 20 {
		v4, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := v4.Addr().(*net.TCPAddr).Port
		v6, err := net.Listen("tcp6", "[::1]:"+strconv.Itoa(port))
		if err == nil {
			return serve(t, v4), serve(t, v6)
		}
		_ = v4.Close()
		if probe, perr := net.Listen("tcp6", "[::1]:0"); perr != nil {
			if os.Getenv("CI") != "" {
				t.Fatalf("no IPv6 loopback: %v", perr)
			}
			t.Skipf("no IPv6 loopback: %v", perr)
		} else {
			_ = probe.Close()
		}
	}
	t.Fatal("no port free on both 127.0.0.1 and ::1")
	return nil, nil
}

func TestPolicyRefusesANameThatResolvesToLoopback(t *testing.T) {
	dns := newFakeDNS(t)
	v4 := listen(t, "127.0.0.1:0")
	dns.set("loop.slicc.test", "127.0.0.1")
	port := strconv.Itoa(v4.port())
	_, err := dialAndGreet(t, dns.dialer(policy.Remote()), "loop.slicc.test.:"+port)
	requireDenied(t, err)
	_, err = dialAndGreet(t, policy.NewDialer(policy.Remote()), "localhost:"+port)
	requireDenied(t, err)
	_, err = dialAndGreet(t, policy.NewDialer(policy.Remote()), "127.0.0.1:"+port)
	requireDenied(t, err)
	v4.assertNoLeak(t)
	if _, err := dialAndGreet(t, dns.dialer(policy.Local()), "loop.slicc.test.:"+port); err != nil {
		t.Fatalf("local default refused loopback: %v", err)
	}
}

func TestPolicyRefusesARebindAfterResolve(t *testing.T) {
	dns := newFakeDNS(t)
	v4 := listen(t, "127.0.0.1:0")
	dns.set("rebind.slicc.test", "93.184.215.14", "127.0.0.1")
	r := &policy.Resolver{Policy: policy.Remote(), Base: dns.resolver()}
	got, err := r.LookupNetIP(context.Background(), "ip4", "rebind.slicc.test.")
	if err != nil || len(got) != 1 || got[0].String() != "93.184.215.14" {
		t.Fatalf("filtered lookup: %v %v", got, err)
	}
	dns.set("rebind.slicc.test", "127.0.0.1")
	_, err = dialAndGreet(t, dns.dialer(policy.Remote()), "rebind.slicc.test.:"+strconv.Itoa(v4.port()))
	requireDenied(t, err)
	v4.assertNoLeak(t)
}

func TestPolicyResolverDropsRefusedAddresses(t *testing.T) {
	dns := newFakeDNS(t)
	dns.set("mixed.slicc.test", "127.0.0.1", "10.0.0.1", "169.254.169.254", "93.184.215.14", "::1", "fd00::1", "2606:2800:21f:cb07:6820:80da:af6b:8b2c", "64:ff9b::a00:1", "64:ff9b:1::808:808", "::808:808", "::ffff:0:808:808")
	dns.set("private.slicc.test", "10.0.0.1", "::1")
	r := &policy.Resolver{Policy: policy.Remote(), Base: dns.resolver()}
	got, err := r.LookupNetIP(context.Background(), "ip", "mixed.slicc.test.")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"93.184.215.14": true, "2606:2800:21f:cb07:6820:80da:af6b:8b2c": true}
	if len(got) != len(want) {
		t.Fatalf("remote kept %v", got)
	}
	for _, a := range got {
		if !want[a.String()] {
			t.Fatalf("remote kept %v", got)
		}
	}
	got, err = r.LookupNetIP(context.Background(), "ip", "private.slicc.test.")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("want an empty list, got %v %v", got, err)
	}
	local := &policy.Resolver{Policy: policy.Local(9222), Base: dns.resolver()}
	got, err = local.LookupNetIP(context.Background(), "ip", "mixed.slicc.test.")
	if err != nil || len(got) != 7 {
		t.Fatalf("local kept %v %v", got, err)
	}
	if _, err := r.LookupNetIP(context.Background(), "ip", "missing.slicc.test."); err == nil {
		t.Fatal("missing name resolved")
	}
}

func TestPolicyPortSpecificDeny(t *testing.T) {
	dns := newFakeDNS(t)
	blocked := listen(t, "127.0.0.1:0")
	open := listen(t, "127.0.0.1:0")
	dns.set("ports.slicc.test", "127.0.0.1")
	deny, err := policy.ParseRules([]string{"127.0.0.1/32:" + strconv.Itoa(blocked.port())})
	if err != nil {
		t.Fatal(err)
	}
	allow, _ := policy.ParseRules([]string{"127.0.0.1/32"})
	for name, p := range map[string]*policy.Policy{
		"remote with flags": policy.Remote().With(allow, deny),
		"local default":     policy.Local(uint16(blocked.port())),
		"local with allow":  policy.Local(uint16(blocked.port())).With(allow, nil),
	} {
		d := dns.dialer(p)
		for _, host := range []string{"127.0.0.1", "ports.slicc.test."} {
			_, err := dialAndGreet(t, d, net.JoinHostPort(host, strconv.Itoa(blocked.port())))
			requireDenied(t, err)
			if _, err := dialAndGreet(t, d, net.JoinHostPort(host, strconv.Itoa(open.port()))); err != nil {
				t.Fatalf("%s: %s on the open port: %v", name, host, err)
			}
		}
	}
	blocked.assertNoLeak(t)
}

func TestPolicyHappyEyeballsSkipsRefusedAddresses(t *testing.T) {
	v4, v6 := dualLoopback(t)
	dns := newFakeDNS(t)
	dns.set("dual.slicc.test", "127.0.0.1", "::1")
	port := strconv.Itoa(v4.port())
	address := "dual.slicc.test.:" + port
	cases := []struct {
		allow   string
		want    *listener
		refused *listener
	}{
		{"[::1]/128:" + port, v6, v4},
		{"127.0.0.1/32:" + port, v4, v6},
	}
	for _, c := range cases {
		allow, err := policy.ParseRules([]string{c.allow})
		if err != nil {
			t.Fatal(err)
		}
		for _, delay := range []time.Duration{0, time.Hour, -1} {
			d := dns.dialer(policy.Remote().With(allow, nil))
			d.Base.FallbackDelay = delay
			got, err := dialAndGreet(t, d, address)
			if err != nil {
				t.Fatalf("allow %s, fallback %v: %v", c.allow, delay, err)
			}
			if got != c.want.Addr().String() {
				t.Fatalf("allow %s: connected to %s", c.allow, got)
			}
		}
		c.refused.assertNoLeak(t)
	}
	_, err := dialAndGreet(t, dns.dialer(policy.Remote()), address)
	requireDenied(t, err)
	v4.assertNoLeak(t)
	v6.assertNoLeak(t)
}

func TestPolicyDialerInHTTPTransport(t *testing.T) {
	dns := newFakeDNS(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok "+r.Host) })
	plain := httptest.NewServer(handler)
	defer plain.Close()
	tlsSrv := httptest.NewTLSServer(handler)
	defer tlsSrv.Close()
	dns.set("web.slicc.test", "127.0.0.1")
	get := func(p *policy.Policy, srv *httptest.Server, host string) (string, error) {
		tr := &http.Transport{DialContext: dns.dialer(p).DialContext}
		if srv.TLS != nil {
			tr.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			tr.TLSClientConfig.ServerName = "example.com"
		}
		defer tr.CloseIdleConnections()
		u := strings.Replace(srv.URL, "127.0.0.1", host, 1)
		resp, err := (&http.Client{Transport: tr, Timeout: 15 * time.Second}).Get(u)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		return string(body), err
	}
	allow, _ := policy.ParseRules([]string{"127.0.0.1/32"})
	for _, srv := range []*httptest.Server{plain, tlsSrv} {
		for _, host := range []string{"127.0.0.1", "web.slicc.test."} {
			if _, err := get(policy.Remote(), srv, host); err == nil {
				t.Fatalf("remote default fetched %s", host)
			} else {
				requireDenied(t, err)
			}
			body, err := get(policy.Remote().With(allow, nil), srv, host)
			if err != nil || !strings.HasPrefix(body, "ok "+host) {
				t.Fatalf("allowed fetch of %s: %q %v", host, body, err)
			}
			port := uint16(srv.Listener.Addr().(*net.TCPAddr).Port)
			_, err = get(policy.Local(port), srv, host)
			requireDenied(t, err)
		}
	}
}

func TestPolicyUserAllowWinsATieWithTheDefault(t *testing.T) {
	v4 := listen(t, "127.0.0.1:0")
	dns := newFakeDNS(t)
	dns.set("tie.slicc.test", "127.0.0.1")
	address := "tie.slicc.test.:" + strconv.Itoa(v4.port())
	_, err := dialAndGreet(t, dns.dialer(policy.Remote()), address)
	requireDenied(t, err)
	tie, err := policy.ParseRules([]string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dialAndGreet(t, dns.dialer(policy.Remote().With(tie, nil)), address); err != nil {
		t.Fatalf("--allow 127.0.0.0/8 did not open its range: %v", err)
	}
	_, err = dialAndGreet(t, dns.dialer(policy.Remote().With(tie, tie)), address)
	requireDenied(t, err)
	optOut, _ := policy.ParseRules([]string{"127.0.0.0/8"})
	_, err = dialAndGreet(t, dns.dialer(policy.Local().With(nil, optOut)), address)
	requireDenied(t, err)
	dns.set("lan.slicc.test", "192.168.7.7", "10.1.2.3", "::1")
	lan, _ := policy.ParseRules([]string{"192.168.0.0/16", "::1/128"})
	r := &policy.Resolver{Policy: policy.Remote().With(lan, nil), Base: dns.resolver()}
	got, err := r.LookupNetIP(context.Background(), "ip", "lan.slicc.test.")
	if err != nil || len(got) != 2 {
		t.Fatalf("tie allows kept %v %v", got, err)
	}
}

func TestPolicyDeniesLocalUseNAT64(t *testing.T) {
	for _, p := range []*policy.Policy{policy.Remote(), policy.Local()} {
		_, err := dialAndGreet(t, policy.NewDialer(p), "[64:ff9b:1::808:808]:443")
		requireDenied(t, err)
		var pe *policy.Error
		if !errors.As(err, &pe) || !strings.Contains(pe.Reason, "64:ff9b:1::/48") {
			t.Fatalf("%v", err)
		}
	}
	if _, err := policy.ParseRule("64:ff9b:1::/48"); err == nil {
		t.Fatal("a rule inside 64:ff9b:1::/48 was accepted")
	}
}

func lanAddress(t *testing.T) netip.Addr {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skipf("no interface addresses: %v", err)
	}
	var v6 []netip.Addr
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(n.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			continue
		}
		if ip.Is4() {
			return ip
		}
		v6 = append(v6, ip)
	}
	if len(v6) > 0 {
		return v6[0]
	}
	if os.Getenv("CI") != "" {
		t.Fatal("the CI runner has no non-loopback address")
	}
	t.Skip("this machine has no non-loopback address")
	return netip.Addr{}
}

func TestPolicyLocalDeniesPortsOnTheMachinesLANAddress(t *testing.T) {
	addr := lanAddress(t)
	blocked := listen(t, netip.AddrPortFrom(addr, 0).String())
	open := listen(t, netip.AddrPortFrom(addr, 0).String())
	allow, err := policy.ParseRules([]string{netip.PrefixFrom(addr, addr.BitLen()).String()})
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(blocked.port())
	for _, p := range []*policy.Policy{policy.Local(port), policy.Local(port).With(allow, nil)} {
		_, err := dialAndGreet(t, policy.NewDialer(p), blocked.Addr().String())
		requireDenied(t, err)
		var pe *policy.Error
		if !errors.As(err, &pe) || !strings.Contains(pe.Reason, "this machine") {
			t.Fatalf("%v", err)
		}
	}
	if _, err := dialAndGreet(t, policy.NewDialer(policy.Local(port).With(allow, nil)), open.Addr().String()); err != nil {
		t.Fatalf("%s: other port refused: %v", addr, err)
	}
	if _, err := dialAndGreet(t, policy.NewDialer(policy.Remote().With(allow, nil)), blocked.Addr().String()); err != nil {
		t.Fatalf("remote default denied a host port: %v", err)
	}
	blocked.assertNoLeak(t)
}
