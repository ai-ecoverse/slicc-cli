package stage1

import (
	"context"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/pion/webrtc/v4"
)

func Open(t *testing.T, handler link.Handler, browser string) (*link.Stream, *link.Stream) {
	t.Helper()
	api := link.NewAPI(link.APIOptions{Loopback: true})
	clientPC, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	serverPC, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	incoming := make(chan *link.Stream, 1)
	server, err := link.New(serverPC, link.RoleNode, link.Options{Hello: link.Hello{Caps: []string{"cdp"}}, Handlers: map[string]link.Handler{"cdp": func(s *link.Stream) { incoming <- s; handler(s) }}})
	if err != nil {
		t.Fatal(err)
	}
	client, err := link.New(clientPC, link.RolePage, link.Options{Hello: link.Hello{Caps: []string{"cdp"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	offer, err := clientPC.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	ready := webrtc.GatheringCompletePromise(clientPC)
	if err = clientPC.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = serverPC.SetRemoteDescription(*clientPC.LocalDescription()); err != nil {
		t.Fatal(err)
	}
	answer, err := serverPC.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	ready = webrtc.GatheringCompletePromise(serverPC)
	if err = serverPC.SetLocalDescription(answer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = clientPC.SetRemoteDescription(*serverPC.LocalDescription()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.Ready():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stream, err := client.Open(ctx, map[string]string{"kind": "cdp", "browser": browser}, link.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case peer := <-incoming:
		return stream, peer
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return nil, nil
}
