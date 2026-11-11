package integration

import (
	"context"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/pion/transport/v5"
	"github.com/pion/webrtc/v4"
)

func addresslessInterfaces() []*transport.Interface {
	return []*transport.Interface{
		transport.NewInterface(net.Interface{Index: 2, Name: "tunl0", Flags: net.FlagUp}),
		transport.NewInterface(net.Interface{Index: 3, Name: "ip6tnl0"}),
		transport.NewInterface(net.Interface{Index: 4, Name: "downbridge"}),
		transport.NewInterface(net.Interface{Index: 5, Name: "gif0", Flags: net.FlagUp}),
	}
}

func TestInterfaceDiscoveryAddresslessInterfaces(t *testing.T) {
	for _, first := range []bool{true, false} {
		name := "addressless last"
		if first {
			name = "addressless first"
		}
		t.Run(name, func(t *testing.T) {
			interfaces := append(syntheticInterfaces(), addresslessInterfaces()...)
			if first {
				interfaces = append(addresslessInterfaces(), syntheticInterfaces()...)
			}
			discovery := link.NewInterfaceDiscovery(func() ([]*transport.Interface, error) { return interfaces, nil }, time.Second)
			got, err := discovery.Interfaces(context.Background())
			if err != nil || !reflect.DeepEqual(got, interfaces) {
				t.Fatalf("mixed interfaces lost: got=%v err=%v", got, err)
			}
			api := link.NewAPI(link.APIOptions{Discovery: discovery, Loopback: true})
			assertDirectDataRoundTrip(t, api)
		})
	}
}

func TestInterfaceDiscoveryAllAddressless(t *testing.T) {
	discovery := link.NewInterfaceDiscovery(func() ([]*transport.Interface, error) { return addresslessInterfaces(), nil }, time.Second)
	interfaces, err := discovery.Interfaces(context.Background())
	if len(interfaces) != 0 || err == nil || err.Error() != "interface probe returned no addresses" {
		t.Fatalf("all-addressless discovery: interfaces=%v err=%v", interfaces, err)
	}
	assertAutomaticRelayConfiguration(t, link.NewAPI(link.APIOptions{Discovery: discovery}))
}

func assertDirectDataRoundTrip(t *testing.T, api *link.API) {
	t.Helper()
	a, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	got := make(chan string, 1)
	fail := make(chan error, 2)
	b.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnMessage(func(m webrtc.DataChannelMessage) {
			if err := dc.Send(m.Data); err != nil {
				fail <- err
			}
		})
	})
	dc, err := a.CreateDataChannel("direct-addressless-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	dc.OnOpen(func() {
		if err := dc.SendText("direct host data"); err != nil {
			fail <- err
		}
	})
	dc.OnMessage(func(m webrtc.DataChannelMessage) { got <- string(m.Data) })
	offer, err := a.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gatherHostDescription(t, a, offer)
	if err := b.SetRemoteDescription(*a.LocalDescription()); err != nil {
		t.Fatal(err)
	}
	answer, err := b.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gatherHostDescription(t, b, answer)
	if err := a.SetRemoteDescription(*b.LocalDescription()); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-got:
		if msg != "direct host data" {
			t.Fatalf("unexpected direct payload %q", msg)
		}
	case err := <-fail:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("direct data channel timed out")
	}
	for _, pc := range []*webrtc.PeerConnection{a, b} {
		pair, err := pc.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
		if err != nil || pair == nil {
			t.Fatalf("missing direct pair: %v", err)
		}
		if pair.Local.Typ != webrtc.ICECandidateTypeHost || pair.Remote.Typ != webrtc.ICECandidateTypeHost {
			t.Fatalf("non-host selected pair: %v", pair)
		}
	}
}

func gatherHostDescription(t *testing.T, pc *webrtc.PeerConnection, description webrtc.SessionDescription) {
	t.Helper()
	if pc.GetConfiguration().ICETransportPolicy != webrtc.ICETransportPolicyAll {
		t.Fatal("healthy discovery forced relay policy")
	}
	done := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(description); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("direct host gathering timed out")
	}
	if !strings.Contains(pc.LocalDescription().SDP, " typ host") {
		t.Fatal("mixed discovery did not preserve host candidates")
	}
}
