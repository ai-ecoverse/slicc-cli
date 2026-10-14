//go:build !wasip1

package facade

import (
	"context"
	"net"
)

func Dial(ctx context.Context, address string) (Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", address)
}

func Ready() error { return nil }
