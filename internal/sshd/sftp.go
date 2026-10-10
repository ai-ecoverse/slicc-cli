package sshd

import (
	"github.com/pkg/sftp"
)

func (s *session) sftp() {
	var opts []sftp.ServerOption
	if s.srv.cfg.Dir != "" {
		opts = append(opts, sftp.WithServerWorkingDirectory(s.srv.cfg.Dir))
	}
	srv, err := sftp.NewServer(s.ch, opts...)
	if err != nil {
		s.fail(1, "slicc: sftp: %v", err)
		return
	}
	go func() {
		code := 0
		if err := srv.Serve(); err != nil {
			code = 1
		}
		s.exit(exitStatus{code: code})
	}()
}
