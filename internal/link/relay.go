package link

import (
	"github.com/pion/stun/v4"
	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/stdnet"
	"github.com/pion/webrtc/v4"
	"strings"
)

type relayNet struct {
	stdnet.Net
}

func (*relayNet) Interfaces() ([]*transport.Interface, error) {
	return nil, nil
}

func (*relayNet) InterfaceByIndex(int) (*transport.Interface, error) {
	return nil, transport.ErrInterfaceNotFound
}

func (*relayNet) InterfaceByName(string) (*transport.Interface, error) {
	return nil, transport.ErrInterfaceNotFound
}

func (*relayNet) UpdateInterfaces() error {
	return nil
}

func tlsRelayServers(servers []webrtc.ICEServer) []webrtc.ICEServer {
	var out []webrtc.ICEServer
	for _, server := range servers {
		credential, ok := server.Credential.(string)
		if server.Username == "" || !ok || credential == "" || server.CredentialType != webrtc.ICECredentialTypePassword {
			continue
		}
		var urls []string
		for _, raw := range server.URLs {
			uri, err := stun.ParseURI(raw)
			if err == nil && !strings.Contains(uri.Host, "%") && uri.Scheme == stun.SchemeTypeTURNS && uri.Proto == stun.ProtoTypeTCP {
				urls = append(urls, raw)
			}
		}
		if len(urls) > 0 {
			server.URLs = urls
			out = append(out, server)
		}
	}
	return out
}
