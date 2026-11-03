//go:build !windows

package integration

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	nativeScreen "github.com/ai-ecoverse/slicc-cli/internal/caps/screen"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

func TestScreenInitialStateWaitsForNodeHello(t *testing.T) {
	for _, mode := range []string{"publish", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			pause := pausedHello{entered: make(chan struct{}), release: make(chan struct{})}
			var once sync.Once
			release := func() { once.Do(func() { close(pause.release) }) }
			defer release()
			states := make(chan nativeScreen.State, 1)
			pair := publicationLinks(t, ctx, link.Options{Hello: link.Hello{Screen: pause}}, link.Options{
				Hello: link.Hello{Caps: []string{nativeScreen.PageCap}},
				Handlers: map[string]link.Handler{nativeScreen.PageCap: func(s *link.Stream) {
					var state nativeScreen.State
					if err := json.Unmarshal(s.OpenPayload(), &state); err != nil {
						t.Error(err)
						_ = s.Reset("EPROTO")
						return
					}
					states <- state
					_ = s.Accept(nil)
					_ = s.CloseWrite()
				}},
			})
			select {
			case <-pause.entered:
			case <-ctx.Done():
				t.Fatal("node HELLO was not paused")
			}
			b := nativeScreen.NewBroker(nil, nil)
			defer b.Close()
			b.Attach(pair.node)
			select {
			case <-pair.page.Done():
				t.Fatal("initial state preceded node HELLO:", pair.page.Err())
			case <-states:
				t.Fatal("initial state arrived before node HELLO")
			case <-time.After(250 * time.Millisecond):
			}
			if mode == "disconnect" {
				_ = pair.node.Close()
				release()
				return
			}
			release()
			select {
			case <-states:
			case <-pair.page.Done():
				t.Fatal("link closed before initial state:", pair.page.Err())
			case <-ctx.Done():
				t.Fatal("initial state did not arrive after node HELLO")
			}
		})
	}
}
