package proxy

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

type echoTransport struct{ targets chan string }

func (e *echoTransport) Start(context.Context) error { return nil }
func (e *echoTransport) Close() error                { return nil }
func (e *echoTransport) DialContext(_ context.Context, _ string, address string) (net.Conn, error) {
	e.targets <- address
	client, server := net.Pipe()
	go func() { defer server.Close(); _, _ = io.Copy(server, server) }()
	return client, nil
}

func TestSOCKS5ConnectAndRelay(t *testing.T) {
	outbound := &echoTransport{targets: make(chan string, 1)}
	server := NewServer("127.0.0.1:0", outbound)
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	conn, err := net.DialTimeout("tcp", server.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("unexpected method reply: %v", reply)
	}

	request := append([]byte{0x05, 0x01, 0x00, 0x03, 0x0b}, []byte("example.com")...)
	request = append(request, 0x01, 0xbb)
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}
	connectReply := make([]byte, 10)
	if _, err := io.ReadFull(conn, connectReply); err != nil {
		t.Fatal(err)
	}
	if connectReply[1] != 0x00 {
		t.Fatalf("connect failed: %v", connectReply)
	}
	if target := <-outbound.targets; target != "example.com:443" {
		t.Fatalf("unexpected target %q", target)
	}

	payload := []byte("easy-net")
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatal(err)
	}
	if string(echo) != string(payload) {
		t.Fatalf("unexpected echo %q", echo)
	}
}

func TestSOCKS5RejectsUnsupportedAuthentication(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- handshake(server) }()
	_, _ = client.Write([]byte{0x05, 0x01, 0x02})
	reply := make([]byte, 2)
	_, _ = io.ReadFull(client, reply)
	if reply[1] != 0xff {
		t.Fatalf("unexpected reply: %v", reply)
	}
	if err := <-done; err == nil {
		t.Fatal("expected handshake error")
	}
}
