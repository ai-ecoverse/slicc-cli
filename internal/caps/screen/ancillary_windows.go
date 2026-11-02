package screen

import "net"

type privateConn struct{ *net.UnixConn }

func (c privateConn) Read([]byte) (int, error) { return 0, ErrAccess }
