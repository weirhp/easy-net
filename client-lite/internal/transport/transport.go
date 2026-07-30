package transport

import (
	"context"
	"net"
)

type Transport interface {
	Start(ctx context.Context) error
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
	Close() error
}
