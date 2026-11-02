//go:build !windows

package integration

import (
	"bytes"
	"context"
	be "encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	capCDP "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	capHTTP "github.com/ai-ecoverse/slicc-cli/internal/caps/http"
	nativeScreen "github.com/ai-ecoverse/slicc-cli/internal/caps/screen"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/pion/webrtc/v4"
)

func screenLinks(t *testing.T, b *nativeScreen.Broker) (*link.Link, <-chan nativeScreen.State) {
	return screenLinksWith(t, b, nil)
}
func screenLinksWith(t *testing.T, b *nativeScreen.Broker, register func(*node.Node)) (*link.Link, <-chan nativeScreen.State) {
	t.Helper()
	states := make(chan nativeScreen.State, 16)
	api := link.NewAPI(link.APIOptions{Loopback: true})
	pc, e := api.NewPeerConnection(webrtc.Configuration{})
	if e != nil {
		t.Fatal(e)
	}
	page, e := link.New(pc, link.RolePage, link.Options{Hello: link.Hello{Caps: []string{nativeScreen.PageCap}}, Handlers: map[string]link.Handler{nativeScreen.PageCap: func(s *link.Stream) {
		var state nativeScreen.State
		if e := json.Unmarshal(s.OpenPayload(), &state); e != nil {
			t.Error(e)
			_ = s.Reset("EPROTO")
			return
		}
		select {
		case states <- state:
		default:
			t.Error("unbounded state")
		}
		_ = s.Accept(nil)
		_ = s.CloseWrite()
	}}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = page.Close() })
	n := node.New(node.Config{})
	if register != nil {
		register(n)
	}
	b.Register(n)
	offer, e := pc.CreateOffer(nil)
	if e != nil {
		t.Fatal(e)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if e = pc.SetLocalDescription(offer); e != nil {
		t.Fatal(e)
	}
	<-gathered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	send := func(_ context.Context, m signal.Message) error {
		if m.T == signal.TypeAnswer {
			return pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP})
		}
		if m.T == signal.TypeCandidate && m.Candidate != nil {
			return pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: m.Candidate.Candidate})
		}
		return nil
	}
	native, e := link.Answer(ctx, api, signal.Message{T: signal.TypeOffer, Nonce: "screen-fixture", SDP: pc.LocalDescription().SDP}, send, n.Options())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { b.Close(); _ = native.Close() })
	b.Attach(native)
	select {
	case <-native.Ready():
	case <-ctx.Done():
		t.Fatal("screen link not ready")
	}
	return page, states
}
func screenState(t *testing.T, ch <-chan nativeScreen.State, active bool) nativeScreen.State {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case state := <-ch:
			if (len(state.Caps) > 0) == active {
				return state
			}
		case <-timer.C:
			t.Fatal("missing screen state")
			return nativeScreen.State{}
		}
	}
}
func screenBinding(b *nativeScreen.Broker) nativeScreen.Binding {
	return nativeScreen.Binding{Link: b.LinkID(), Session: nativeScreen.ID(16), Source: nativeScreen.ID(16), GrantEpoch: "1", SourceEpoch: "1", ProtectionEpoch: "1"}
}
func screenJPEG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if e := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestScreenGrantFrameStopAndNoContentAudit(t *testing.T) {
	dir := t.TempDir()
	j, e := nativeScreen.NewJournal(filepath.Join(dir, "cli"), filepath.Join(dir, "app"))
	if e != nil {
		t.Fatal(e)
	}
	revoked := make(chan struct{}, 1)
	b := nativeScreen.NewBroker(j.Record, func(string) { revoked <- struct{}{} })
	page, states := screenLinks(t, b)
	initial := screenState(t, states, false)
	if initial.LastSession != nil || len(initial.Caps) != 0 {
		t.Fatal("default grant")
	}
	binding := screenBinding(b)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, e = page.Open(ctx, nativeScreen.Open{Kind: "screen", V: 1, Binding: binding}, link.OpenOptions{}); e == nil {
		t.Fatal("screen without consent")
	}
	if e = b.Grant(binding, "window", nativeScreen.DefaultLimits()); e != nil {
		t.Fatal(e)
	}
	state := screenState(t, states, true)
	if state.Binding == nil || *state.Binding != binding {
		t.Fatal("binding mismatch")
	}
	s, e := page.Open(ctx, nativeScreen.Open{Kind: "screen", V: 1, Binding: binding, MaxFPS: 5, MaxWidth: 2, MaxHeight: 2}, link.OpenOptions{})
	if e != nil {
		t.Fatal(e)
	}
	_ = s.CloseWrite()
	body := screenJPEG(t)
	f := nativeScreen.Frame{Type: "frame", V: 1, Binding: binding, Seq: "1", Captured: "1", Width: 2, Height: 2, Codec: "jpeg"}
	sent := make(chan error, 1)
	go func() { sent <- b.Frame(f, body) }()
	var got nativeScreen.Frame
	received, e := nativeScreen.ReadRecord(s.Conn(), &got, nativeScreen.FrameBytes, time.Second)
	if e != nil || !bytes.Equal(body, received) {
		t.Fatalf("frame transport: %v", e)
	}
	if e = <-sent; e != nil {
		t.Fatal(e)
	}
	if e = b.Frame(f, body); !errors.Is(e, nativeScreen.ErrStale) {
		t.Fatalf("sequence replay: %v", e)
	}
	stop, e := page.Open(ctx, nativeScreen.Stop{Kind: "screen-stop", V: 1, Link: binding.Link, Session: &binding.Session}, link.OpenOptions{Shared: true})
	if e != nil {
		t.Fatal(e)
	}
	_ = stop.CloseWrite()
	select {
	case <-revoked:
	case <-ctx.Done():
		t.Fatal("no native revoke")
	}
	disabled := screenState(t, states, false)
	if disabled.Binding != nil || len(disabled.Caps) != 0 || disabled.LastSession == nil {
		t.Fatal("retained authority")
	}
	if e = b.Frame(nativeScreen.Frame{Type: "frame", V: 1, Binding: binding, Seq: "2", Captured: "2", Width: 2, Height: 2, Codec: "jpeg"}, body); !errors.Is(e, nativeScreen.ErrAccess) {
		t.Fatal("late frame accepted")
	}
	for _, name := range []string{"cli", "app"} {
		entries, e := os.ReadDir(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range entries {
			data, e := os.ReadFile(filepath.Join(dir, name, entry.Name()))
			if e != nil {
				t.Fatal(e)
			}
			for _, secret := range [][]byte{body, []byte("PRIVATE_SCREEN_SENTINEL"), []byte("capturedMonoMs"), []byte("frameSeq")} {
				if len(secret) > 0 && bytes.Contains(data, secret) {
					t.Fatal("content leaked to audit")
				}
			}
		}
	}
}
func TestScreenStrictCountersAndRecordLimits(t *testing.T) {
	for _, s := range []string{"", "01", "-1", "1e2", "18446744073709551616", "1.0"} {
		if _, e := nativeScreen.Counter(s).Value(); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	if _, e := nativeScreen.Counter("18446744073709551615").Next(); e == nil {
		t.Fatal("overflow wrapped")
	}
	for _, raw := range []string{`{"kind":"screen","kind":"screen"}`, `{"kind":"screen","v":1,"unknown":true}`, `{"kind":"screen","binding":{"link":"a","link":"b"}}`} {
		var o nativeScreen.Open
		if e := nativeScreen.Strict([]byte(raw), &o, 4096); e == nil {
			t.Fatal("ambiguous JSON")
		}
	}
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	defer func() { _ = b.Close() }()
	go func() { _, _ = b.Write([]byte{0, 0, 16, 1}) }()
	var f nativeScreen.Frame
	if _, e := nativeScreen.ReadRecord(a, &f, nativeScreen.FrameBytes, 30*time.Millisecond); !errors.Is(e, nativeScreen.ErrSize) {
		t.Fatalf("length admission: %v", e)
	}
	c, d := net.Pipe()
	defer func() { _ = c.Close() }()
	defer func() { _ = d.Close() }()
	go func() { _, _ = d.Write([]byte{0}) }()
	start := time.Now()
	if _, e := nativeScreen.ReadRecord(c, &f, nativeScreen.FrameBytes, 30*time.Millisecond); e == nil || time.Since(start) > time.Second {
		t.Fatal("partial record deadline")
	}
}
func TestScreenFilterBuilderInvariantFixture(t *testing.T) {
	data, e := os.ReadFile(filepath.Join("testdata", "screen", "filter-builder.json"))
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		V       int `json:"v"`
		Vectors []struct {
			Name       string            `json:"name"`
			Included   []string          `json:"included"`
			Exceptions []json.RawMessage `json:"exceptingWindows"`
			Allowed    bool              `json:"allowed"`
		} `json:"vectors"`
		Invariant string `json:"invariant"`
		Native    bool   `json:"nativeBuilderMustConsume"`
	}
	if e = nativeScreen.Strict(data, &f, 4096); e != nil || !f.Native || len(f.Vectors) < 2 {
		t.Fatal("invalid native invariant fixture")
	}
	for _, v := range f.Vectors {
		if v.Allowed != (len(v.Exceptions) == 0) {
			t.Fatal("filter exception reversal")
		}
	}
}
func TestScreenUnsignedPeerRefusedBeforeFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("local IPC")
	}
	if os.PathSeparator == '\\' {
		t.Skip("Darwin AF_UNIX bootstrap")
	}
	dir, e := os.MkdirTemp("", "slicc-screen-refusal-")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "s")
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = listener.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := listener.AcceptUnix()
		if e != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		var one [1]byte
		n, _ := c.Read(one[:])
		if n != 0 {
			t.Error("data sent before peer authentication")
		}
	}()
	bootstrap := nativeScreen.Bootstrap{V: 1, Launch: nativeScreen.ID(32), Challenge: nativeScreen.ID(32), SafetyPath: path, FramesPath: filepath.Join(dir, "f"), ParentAuditToken: (nativeScreen.Peer{}).Encoded()}
	calls := 0
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, e = nativeScreen.Connect(ctx, bootstrap, func(*net.UnixConn, nativeScreen.Peer) error { calls++; return nativeScreen.ErrAccess }, func(string, string, string, string) error { return nil })
	if !errors.Is(e, nativeScreen.ErrAccess) || calls != 1 {
		t.Fatalf("unsigned peer admitted: %v calls %d", e, calls)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("untrusted connection retained")
	}
}
func TestScreenStopDoesNotWaitForAudit(t *testing.T) {
	blocked := make(chan struct{})
	entered := make(chan struct{})
	defer close(blocked)
	b := nativeScreen.NewBroker(func(event, _, _, _ string) error {
		if event == "stop" {
			close(entered)
			<-blocked
		}
		return nil
	}, nil)
	_, states := screenLinks(t, b)
	screenState(t, states, false)
	binding := screenBinding(b)
	if e := b.Grant(binding, "display", nativeScreen.DefaultLimits()); e != nil {
		t.Fatal(e)
	}
	screenState(t, states, true)
	returned := make(chan struct{})
	go func() { b.Revoke("stopped"); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("stop waited on audit")
	}
	if len(b.Snapshot().Caps) != 0 {
		t.Fatal("stop kept cap")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("missing stop audit")
	}
}

type screenApp struct {
	mu         sync.Mutex
	safety     *net.UnixConn
	frames     *net.UnixConn
	launch     string
	connection string
	messages   chan map[string]json.RawMessage
	quit       chan struct{}
}

func (a *screenApp) send(t *testing.T, fields map[string]any) {
	t.Helper()
	if !a.reply(fields) {
		t.Fatal("private app send failed")
	}
}
func (a *screenApp) reply(fields map[string]any) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	fields["v"] = 1
	fields["launch"] = a.launch
	fields["connection"] = a.connection
	return nativeScreen.WriteRecord(a.safety, fields, nil, time.Second) == nil
}
func newScreenApp(t *testing.T) (*nativeScreen.Client, *screenApp) {
	t.Helper()
	dir, e := os.MkdirTemp("", "screen-ipc-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	bootstrap := nativeScreen.Bootstrap{V: 1, Launch: nativeScreen.ID(32), Challenge: nativeScreen.ID(32), SafetyPath: filepath.Join(dir, "s"), FramesPath: filepath.Join(dir, "f"), ParentAuditToken: (nativeScreen.Peer{}).Encoded()}
	a := &screenApp{launch: bootstrap.Launch, connection: nativeScreen.ID(32), messages: make(chan map[string]json.RawMessage, 16), quit: make(chan struct{})}
	sl, e := net.ListenUnix("unix", &net.UnixAddr{Name: bootstrap.SafetyPath, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	fl, e := net.ListenUnix("unix", &net.UnixAddr{Name: bootstrap.FramesPath, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	initialized := make(chan struct{})
	var initializedOnce sync.Once
	t.Cleanup(func() {
		_ = sl.Close()
		_ = fl.Close()
		<-initialized
		if a.safety != nil {
			_ = a.safety.Close()
		}
		if a.frames != nil {
			_ = a.frames.Close()
		}
	})
	auth := func(listener *net.UnixListener) *net.UnixConn {
		c, e := listener.AcceptUnix()
		if e != nil {
			return nil
		}
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		var n [4]byte
		if _, e = io.ReadFull(c, n[:]); e != nil {
			_ = c.Close()
			return nil
		}
		size := be.BigEndian.Uint32(n[:])
		if size > 4096 {
			_ = c.Close()
			return nil
		}
		p := make([]byte, int(size))
		if _, e = io.ReadFull(c, p); e != nil {
			_ = c.Close()
			return nil
		}
		var hello map[string]any
		if json.Unmarshal(p, &hello) != nil {
			_ = c.Close()
			return nil
		}
		hello["serverNonce"] = nativeScreen.ID(32)
		hello["connection"] = a.connection
		response, _ := json.Marshal(hello)
		be.BigEndian.PutUint32(n[:], uint32(len(response)))
		if _, e = c.Write(n[:]); e != nil {
			_ = c.Close()
			return nil
		}
		if _, e = c.Write(response); e != nil {
			_ = c.Close()
			return nil
		}
		return c
	}
	go func() {
		defer close(a.quit)
		defer initializedOnce.Do(func() { close(initialized) })
		a.safety = auth(sl)
		if a.safety == nil {
			return
		}
		a.frames = auth(fl)
		if a.frames == nil {
			return
		}
		initializedOnce.Do(func() { close(initialized) })
		seq := uint64(0)
		for {
			var message map[string]json.RawMessage
			if _, e := nativeScreen.ReadRecord(a.safety, &message, 0, time.Second); e != nil {
				return
			}
			var kind string
			_ = json.Unmarshal(message["type"], &kind)
			switch kind {
			case "heartbeat":
				seq++
				var echo string
				_ = json.Unmarshal(message["seq"], &echo)
				if !a.reply(map[string]any{"type": "heartbeat", "seq": strconv.FormatUint(seq, 10), "echo": echo}) {
					return
				}
			case "revoke":
				var request string
				_ = json.Unmarshal(message["request"], &request)
				if !a.reply(map[string]any{"type": "revoked", "request": request, "link": message["link"], "session": message["session"]}) {
					return
				}
			default:
				select {
				case a.messages <- message:
				default:
					return
				}
			}
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, e := nativeScreen.Connect(ctx, bootstrap, func(*net.UnixConn, nativeScreen.Peer) error { return nil }, func(string, string, string, string) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	<-initialized
	t.Cleanup(c.Close)
	return c, a
}
func TestScreenPrivateHandshakeGrantAndLease(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("native Unix IPC")
	}
	c, a := newScreenApp(t)
	b := c.Broker()
	page, states := screenLinks(t, b)
	screenState(t, states, false)
	binding := screenBinding(b)
	a.send(t, map[string]any{"type": "grant", "request": nativeScreen.ID(16), "binding": binding, "scope": "window", "limits": nativeScreen.DefaultLimits()})
	screenState(t, states, true)
	select {
	case m := <-a.messages:
		var kind string
		_ = json.Unmarshal(m["type"], &kind)
		if kind != "granted" {
			t.Fatal("images before grant ack")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no grant confirmation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, e := page.Open(ctx, nativeScreen.Open{Kind: "screen", V: 1, Binding: binding}, link.OpenOptions{})
	if e != nil {
		t.Fatal(e)
	}
	_ = stream.CloseWrite()
	frame := nativeScreen.Frame{Type: "frame", V: 1, Binding: binding, Seq: "1", Captured: "1", Width: 2, Height: 2, Codec: "jpeg", Launch: a.launch, Connection: a.connection}
	if e = nativeScreen.WriteRecord(a.frames, frame, screenJPEG(t), time.Second); e != nil {
		t.Fatal(e)
	}
	var got nativeScreen.Frame
	if _, e = nativeScreen.ReadRecord(stream.Conn(), &got, nativeScreen.FrameBytes, time.Second); e != nil {
		t.Fatal(e)
	}
	if got.Launch != "" || got.Connection != "" {
		t.Fatal("private binding leaked")
	}
	_ = a.safety.Close()
	screenState(t, states, false)
	if len(b.Snapshot().Caps) != 0 {
		t.Fatal("IPC EOF kept grant")
	}
}

func TestScreenLargeFrameCreditsAndStopBeforeBlockedWriter(t *testing.T) {
	b := nativeScreen.NewBroker(func(string, string, string, string) error { return nil }, nil)
	page, states := screenLinks(t, b)
	screenState(t, states, false)
	binding := screenBinding(b)
	if e := b.Grant(binding, "display", nativeScreen.DefaultLimits()); e != nil {
		t.Fatal(e)
	}
	screenState(t, states, true)
	pixels := image.NewRGBA(image.Rect(0, 0, 1024, 1024))
	seed := uint32(37)
	for i := range pixels.Pix {
		seed = 1664525*seed + 1013904223
		pixels.Pix[i] = byte(seed >> 24)
	}
	var out bytes.Buffer
	if e := jpeg.Encode(&out, pixels, &jpeg.Options{Quality: 90}); e != nil {
		t.Fatal(e)
	}
	body := out.Bytes()
	if len(body) <= link.Window || len(body) > nativeScreen.FrameBytes {
		t.Fatal("fixture must cross credit window")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, e := page.Open(ctx, nativeScreen.Open{Kind: "screen", V: 1, Binding: binding}, link.OpenOptions{})
	if e != nil {
		t.Fatal(e)
	}
	_ = s.CloseWrite()
	frame := nativeScreen.Frame{Type: "frame", V: 1, Binding: binding, Seq: "1", Captured: "1", Width: 1024, Height: 1024, Codec: "jpeg"}
	done := make(chan error, 1)
	go func() { done <- b.Frame(frame, body) }()
	var got nativeScreen.Frame
	data, e := nativeScreen.ReadRecord(s.Conn(), &got, nativeScreen.FrameBytes, 2*time.Second)
	if e != nil || !bytes.Equal(data, body) {
		t.Fatalf("credit-window transport: %v", e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	frame.Seq = "2"
	<-time.After(220 * time.Millisecond)
	go func() { done <- b.Frame(frame, body) }()
	_ = s.SetReadDeadline(time.Now().Add(time.Second))
	var first [1]byte
	if _, e = io.ReadFull(s, first[:]); e != nil {
		t.Fatal("second record did not start", e)
	}
	select {
	case <-done:
		t.Fatal("multi-window writer finished without credit")
	default:
	}
	start := time.Now()
	b.Revoke("stopped")
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("stop waited for bulk transport")
	}
	if len(b.Snapshot().Caps) != 0 {
		t.Fatal("stop did not remove effective cap")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("blocked writer survived stop")
	}
}

func TestScreenStopDuringAuditCannotRestoreGrant(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	b := nativeScreen.NewBroker(func(event, _, _, _ string) error {
		if event == "grant-intent" {
			close(entered)
			<-release
		}
		return nil
	}, nil)
	_, states := screenLinks(t, b)
	screenState(t, states, false)
	binding := screenBinding(b)
	done := make(chan error, 1)
	go func() { done <- b.Grant(binding, "window", nativeScreen.DefaultLimits()) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("grant intent not written")
	}
	stopped := make(chan struct{})
	go func() { b.Revoke("stopped"); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(500 * time.Millisecond):
		close(release)
		t.Fatal("stop waited on grant audit")
	}
	close(release)
	if e := <-done; e == nil {
		t.Fatal("late intent restored a stopped grant")
	}
	if len(b.Snapshot().Caps) != 0 {
		t.Fatal("stopped cap restored")
	}
}

func TestScreenRevokeIndependentOfFullHTTPCDPBuffers(t *testing.T) {
	fake := newFakeCDPBrowser(t, func(string, json.RawMessage) (string, []string) { return "{}", nil })
	registry := capCDP.NewRegistry(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	browser, e := registry.Add(ctx, fake.srv.URL, false)
	if e != nil {
		t.Fatal(e)
	}
	c, a := newScreenApp(t)
	b := c.Broker()
	page, states := screenLinksWith(t, b, func(n *node.Node) {
		capHTTP.RegisterOptions(n, capHTTP.Options{Dialer: &net.Dialer{Timeout: time.Second}})
		capCDP.Register(n, registry)
	})
	screenState(t, states, false)
	binding := screenBinding(b)
	a.send(t, map[string]any{"type": "grant", "request": nativeScreen.ID(16), "binding": binding, "scope": "window", "limits": nativeScreen.DefaultLimits()})
	screenState(t, states, true)
	running := make(chan struct{})
	framesDone := make(chan struct{})
	body := screenJPEG(t)
	go func() {
		defer close(framesDone)
		seq := uint64(1)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-running:
				return
			case <-ticker.C:
				frame := nativeScreen.Frame{Type: "frame", V: 1, Binding: binding, Seq: nativeScreen.Counter(strconv.FormatUint(seq, 10)), Captured: "1", Width: 2, Height: 2, Codec: "jpeg", Launch: a.launch, Connection: a.connection}
				seq++
				if nativeScreen.WriteRecord(a.frames, frame, body, time.Second) != nil {
					return
				}
			}
		}
	}()
	t.Cleanup(func() { close(running); <-framesDone })
	httpStream, e := page.Open(ctx, map[string]any{"kind": "http"}, link.OpenOptions{})
	if e != nil {
		t.Fatal(e)
	}
	var size [4]byte
	be.BigEndian.PutUint32(size[:], capHTTP.MaxHead)
	if _, e = httpStream.Write(size[:]); e != nil {
		t.Fatal(e)
	}
	if _, e = httpStream.Write(bytes.Repeat([]byte{'x'}, capHTTP.MaxHead-1)); e != nil {
		t.Fatal(e)
	}
	cdpStream, e := page.Open(ctx, map[string]any{"kind": "cdp", "browser": browser.ID}, link.OpenOptions{})
	if e != nil {
		t.Fatal(e)
	}
	be.BigEndian.PutUint32(size[:], capCDP.MaxMessage)
	if _, e = cdpStream.Write(size[:]); e != nil {
		t.Fatal(e)
	}
	chunk := bytes.Repeat([]byte{'x'}, 1<<20)
	remaining := capCDP.MaxMessage - 1
	for remaining > 0 {
		n := min(remaining, len(chunk))
		if _, e = cdpStream.Write(chunk[:n]); e != nil {
			t.Fatal(e)
		}
		remaining -= n
	}
	if len(b.Snapshot().Caps) != 1 {
		t.Fatal("buffer load revoked healthy provider before stop")
	}
	request := nativeScreen.ID(16)
	start := time.Now()
	a.send(t, map[string]any{"type": "revoke", "request": request, "link": binding.Link, "session": binding.Session, "reason": "stopped"})
	deadline := time.NewTimer(500 * time.Millisecond)
	defer deadline.Stop()
	confirmed := false
	for !confirmed {
		select {
		case m := <-a.messages:
			var kind, reply string
			_ = json.Unmarshal(m["type"], &kind)
			_ = json.Unmarshal(m["request"], &reply)
			confirmed = kind == "revoked" && reply == request
		case <-deadline.C:
			t.Fatal("native safety revoke waited on old-cap buffers")
		}
	}
	t.Logf("synthetic full-buffer safety revoke and confirmation: %s", time.Since(start))
	if len(b.Snapshot().Caps) != 0 {
		t.Fatal("old cap buffers retained screen authority")
	}
	if httpStream.Err() != nil || cdpStream.Err() != nil {
		t.Fatal("screen revoke reset an existing old-cap stream")
	}
	_ = httpStream.Reset("ECANCELED")
	_ = cdpStream.Reset("ECANCELED")
}

func TestScreenOldStopDoesNotRevokeNewLocalGrant(t *testing.T) {
	b := nativeScreen.NewBroker(func(string, string, string, string) error { return nil }, nil)
	page, states := screenLinks(t, b)
	screenState(t, states, false)
	old := screenBinding(b)
	if e := b.Grant(old, "window", nativeScreen.DefaultLimits()); e != nil {
		t.Fatal(e)
	}
	screenState(t, states, true)
	b.Revoke("stopped")
	screenState(t, states, false)
	fresh := screenBinding(b)
	fresh.GrantEpoch = "2"
	if e := b.Grant(fresh, "window", nativeScreen.DefaultLimits()); e != nil {
		t.Fatal(e)
	}
	screenState(t, states, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, e := page.Open(ctx, nativeScreen.Stop{Kind: "screen-stop", V: 1, Link: old.Link, Session: &old.Session}, link.OpenOptions{Shared: true})
	if e == nil || !strings.Contains(e.Error(), "ESTALE") {
		t.Fatalf("old stop: %v", e)
	}
	state := b.Snapshot()
	if len(state.Caps) != 1 || state.Binding == nil || state.Binding.Session != fresh.Session {
		t.Fatal("stale stop revoked the new local grant")
	}
}

func TestScreenSharedAuditRetentionIgnoresRefreshedMtime(t *testing.T) {
	root := t.TempDir()
	broker, app := filepath.Join(root, "broker"), filepath.Join(root, "app")
	journal, e := nativeScreen.NewJournal(broker, app)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	expired := now.Add(-31 * 24 * time.Hour).Format("20060102")
	paths := []string{
		filepath.Join(broker, "broker-"+expired+"-old.jsonl"),
		filepath.Join(app, "app-"+expired+"-old.jsonl"),
		filepath.Join(app, "app-"+now.Format("20060102")+"-early.jsonl"),
		filepath.Join(app, "prototype.jsonl"),
	}
	for _, p := range paths {
		if e = os.WriteFile(p, []byte("PRIVATE_RETIRED_AUDIT_SENTINEL"), 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.Chtimes(p, now, now); e != nil {
			t.Fatal(e)
		}
	}
	earlier := now.Add(-31 * 24 * time.Hour)
	if e = os.Chtimes(paths[2], earlier, earlier); e != nil {
		t.Fatal(e)
	}
	if e = journal.Record("stop", "stopped", "", ""); e != nil {
		t.Fatal(e)
	}
	for _, p := range paths {
		if _, e = os.Stat(p); !errors.Is(e, os.ErrNotExist) {
			t.Fatalf("retained expired or undated audit %s: %v", filepath.Base(p), e)
		}
	}
}

func TestScreenAssociationQuotaPreservesExistingStreams(t *testing.T) {
	b := nativeScreen.NewBroker(func(string, string, string, string) error { return nil }, nil)
	entered := make(chan *link.Stream, 32)
	page, states := screenLinksWith(t, b, func(n *node.Node) { n.HandleKind("hold", func(s *link.Stream) { entered <- s; <-s.Done() }) })
	screenState(t, states, false)
	binding := screenBinding(b)
	if e := b.Grant(binding, "window", nativeScreen.DefaultLimits()); e != nil {
		t.Fatal(e)
	}
	screenState(t, states, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan struct{}, 32)
	held := make([]*link.Stream, 0, 32)
	for range 32 {
		go func() {
			_, _ = page.Open(ctx, map[string]any{"kind": "hold"}, link.OpenOptions{Shared: true})
			finished <- struct{}{}
		}()
		select {
		case s := <-entered:
			held = append(held, s)
		case <-ctx.Done():
			t.Fatal("pending admission below agreed floor")
		}
	}
	_, e := page.Open(ctx, map[string]any{"kind": "hold"}, link.OpenOptions{Shared: true})
	var reset *link.ResetError
	if !errors.As(e, &reset) || reset.Code != "EBUSY" {
		t.Fatal("quota OPEN was not refused", e)
	}
	state := screenState(t, states, false)
	if state.Reason != "resource-limit" {
		t.Fatal("quota did not invalidate screen")
	}
	for _, s := range held {
		if s.Err() != nil {
			t.Fatal("quota reset an existing stream")
		}
	}
	select {
	case <-page.Done():
		t.Fatal("ordinary quota breach closed association")
	default:
	}
	fresh := screenBinding(b)
	fresh.GrantEpoch = "2"
	if !errors.Is(b.Grant(fresh, "window", nativeScreen.DefaultLimits()), nativeScreen.ErrAccess) {
		t.Fatal("quota allowed screen to resume")
	}
	cancel()
	for range 32 {
		<-finished
	}
}
