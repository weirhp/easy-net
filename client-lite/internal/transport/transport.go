package transport

import (
	"context"
	"errors"
	"net"
	"time"
)

type Transport interface {
	Start(ctx context.Context) error
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
	Close() error
}

var ErrPacketUnsupported = errors.New("当前传输不支持 UDP")

type PacketConn interface {
	ReadPacket(buffer []byte) (size int, source string, err error)
	WritePacket(payload []byte, destination string) error
	SetDeadline(deadline time.Time) error
	Close() error
}

// PacketTransport is optional. WebSocket implements it, while standard SSH
// dynamic forwarding has no UDP channel and deliberately does not.
type PacketTransport interface {
	OpenPacketContext(ctx context.Context) (PacketConn, error)
}

func OpenPacketContext(ctx context.Context, outbound Transport) (PacketConn, error) {
	packetTransport, ok := outbound.(PacketTransport)
	if !ok {
		return nil, ErrPacketUnsupported
	}
	return packetTransport.OpenPacketContext(ctx)
}
