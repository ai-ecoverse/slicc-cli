package cdp

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	client "github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func TestOrdinaryFormGuardPlacement(t *testing.T) {
	g := newGuard(protect.MustNew(nil), func(string, ...any) {})
	g.sessions["s"] = &session{id: "s", target: "t"}
	g.targets["t"] = &target{id: "t", typ: "page", url: "https://billing.example", shown: true}
	nt := &navTarget{target: "t", session: "guard", done: true, fetchArmed: true, loaderID: "doc"}
	g.navTargets["t"] = nt
	d := formDispatch{"t", "s", "world", "doc"}
	msg := []byte(`{"id":1,"sessionId":"s","method":"Runtime.evaluate","params":{"uniqueContextId":"world"}}`)
	if !g.checkFormDispatch(d, msg) {
		t.Fatal("expected eligible placement")
	}
	g.extensionFrameURLs = map[string]map[string]string{"t": {"extension": "chrome-extension://test/"}}
	if g.checkFormDispatch(d, msg) {
		t.Fatal("extension iframe admitted")
	}
	delete(g.extensionFrameURLs, "t")
	nt.loaderID = "next"
	if g.checkFormDispatch(d, msg) {
		t.Fatal("loader mismatch")
	}
	nt.loaderID = "doc"
	d.world = "other"
	if g.checkFormDispatch(d, msg) {
		t.Fatal("wrong execution context")
	}
	d.world = "world"
	nt.historyLocked = true
	if g.checkFormDispatch(d, msg) {
		t.Fatal("protected history")
	}
	nt.historyLocked = false
	nt.fetchArmed = false
	if g.checkFormDispatch(d, msg) {
		t.Fatal("missing protected document guard")
	}
}
func TestOrdinaryFormGuardRefusalReturnsLocalError(t *testing.T) {
	g := newGuard(protect.MustNew(nil), func(string, ...any) {})
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	s := &localStream{Conn: a, done: make(chan struct{})}
	rl := &relay{s: s, g: g}
	c := client.NewClient(&localTransport{s: s, c: b}, nil)
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	g.sessions["s"] = &session{id: "s", target: "t"}
	g.targets["t"] = &target{id: "t", typ: "page", url: "https://billing.example", shown: true}
	ctx = FormDispatch(ctx, "t", "s", "world", "loader")
	go func() {
		var msg json.RawMessage
		tport := &localTransport{c: a}
		_, msg, _ = tport.Read(ctx)
		cmd, _, _ := parseCommand(msg)
		g.mu.Lock()
		out := g.admitLocked(cmd).forward
		g.mu.Unlock()
		_ = rl.forward(ctx, msg, out)
	}()
	if err := c.Call(ctx, "s", "Runtime.evaluate", map[string]any{"uniqueContextId": "world"}, nil); err == nil || ctx.Err() != nil {
		t.Fatal("expected immediate refusal before browser dispatch", err)
	}
}
