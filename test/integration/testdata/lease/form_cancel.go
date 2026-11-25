package lease

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/coder/websocket"
)

type formCDP struct {
	reply   chan []byte
	sent    chan struct{}
	release chan struct{}
	closed  chan struct{}
}

func (f *formCDP) Close(websocket.StatusCode, string) error { close(f.closed); return nil }
func (f *formCDP) Read(context.Context) (websocket.MessageType, []byte, error) {
	select {
	case data := <-f.reply:
		return websocket.MessageText, data, nil
	case <-f.closed:
		return 0, nil, context.Canceled
	}
}
func (f *formCDP) Write(_ context.Context, _ websocket.MessageType, data []byte) error {
	var q struct {
		ID     int
		Method string
	}
	if e := json.Unmarshal(data, &q); e != nil {
		return e
	}
	var result string
	switch q.Method {
	case "Page.getFrameTree":
		result = `{"frameTree":{"frame":{"id":"tab","loaderId":"doc","securityOrigin":"https://example.test","url":"https://example.test/"}}}`
	case "Page.createIsolatedWorld":
		result = `{"executionContextId":1}`
	case "Runtime.evaluate":
		result = `{"result":{"value":{"status":"ok"}}}`
		close(f.sent)
	default:
		return fmt.Errorf("unexpected method %s", q.Method)
	}
	body := []byte(fmt.Sprintf(`{"id":%d,"result":%s}`, q.ID, result))
	if q.Method == "Runtime.evaluate" {
		go func() { <-f.release; f.reply <- body }()
	} else {
		f.reply <- body
	}
	return nil
}
func TestParkedFormResultCannotOutliveAuthority(t *testing.T) {
	for _, name := range []string{"revoke", "expire", "blocked-result"} {
		t.Run(name, func(t *testing.T) {
			exp := time.Now().Unix() + 120
			if name == "expire" {
				exp = time.Now().Unix() + 2
			}
			ctx, cancel := context.WithDeadline(t.Context(), time.Unix(exp, 0))
			defer cancel()
			f := &formCDP{reply: make(chan []byte, 2), sent: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
			bs := &browserSession{c: cdp.NewClient(f, nil)}
			defer func() { _ = bs.c.Close() }()
			rule := Rule{Kind: "origin", URL: "https://example.test"}
			c := claims{ID: "a", Allow: []Rule{rule}, Exp: exp}
			p := "e" + id()
			tgt := &tab{target: "tab", session: "s", world: "ordinary", unique: "unique", context: 1}
			a := &active{claims: c, tab: tgt, ctx: ctx, cancel: cancel, formEnd: rule.URL + "/submit", refs: refSet{prefix: p, world: "unique", count: 2, at: stamp{f: frame{ID: "tab", LoaderID: "doc", URL: rule.URL + "/", SecurityOrigin: rule.URL}}}}
			a.timer = time.AfterFunc(time.Hour, func() {})
			defer a.timer.Stop()
			b := &Broker{active: map[string]*active{"a": a}, bs: bs}
			returned := make(chan bool, 1)
			done := make(chan error, 1)
			go func() {
				done <- b.action(t.Context(), a, "click", json.RawMessage(`{"ref":"`+p+`_1"}`), func(any) bool {
					if name == "blocked-result" {
						return false
					}
					returned <- true
					return true
				})
			}()
			select {
			case <-f.sent:
			case e := <-done:
				t.Fatalf("unexpected refusal %v", e)
			case <-time.After(time.Second):
				t.Fatal("did not dispatch")
			}
			if name == "expire" {
				<-ctx.Done()
			} else if name == "revoke" {
				b.mu.Lock()
				b.retire(a.ID, true)
				b.mu.Unlock()
			}
			close(f.release)
			if e := <-done; e != unknown {
				t.Fatalf("stale result %v", e)
			}
			if !a.submitted {
				t.Fatal("did not consume submit budget")
			}
			select {
			case <-returned:
				t.Fatal("delivered stale success")
			default:
			}
		})
	}
}
