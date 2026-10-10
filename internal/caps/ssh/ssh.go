package ssh

import (
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/sshd"
)

const (
	Kind = "ssh"
	Cap  = "ssh"
)

func Register(n *node.Node, srv *sshd.Server) {
	n.SetSSHHostKey(sshd.AuthorizedKey(srv.HostKey()))
	n.Handle(Cap, Kind, Handler(srv, n.Logf))
}

func Handler(srv *sshd.Server, logf func(format string, args ...any)) link.Handler {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return func(s *link.Stream) {
		if err := s.Accept(nil); err != nil {
			return
		}
		if err := srv.ServeConn(s.Conn()); err != nil {
			logf("ssh stream %d: %v", s.ID(), err)
		}
	}
}
