package launch

import (
	"io"
	"net"
	"testing"
)

func TestCheckSOCKS5Proxy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer connection.Close()
		greeting := make([]byte, 3)
		if _, readErr := io.ReadFull(connection, greeting); readErr != nil {
			done <- readErr
			return
		}
		if _, writeErr := connection.Write([]byte{5, 0}); writeErr != nil {
			done <- writeErr
			return
		}
		head := make([]byte, 5)
		if _, readErr := io.ReadFull(connection, head); readErr != nil {
			done <- readErr
			return
		}
		hostAndPort := make([]byte, int(head[4])+2)
		if _, readErr := io.ReadFull(connection, hostAndPort); readErr != nil {
			done <- readErr
			return
		}
		_, writeErr := connection.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		done <- writeErr
	}()
	if err := checkSOCKS5Proxy(listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCheckSOCKS5ProxyRejectsFailedConnect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		greeting := make([]byte, 3)
		_, _ = io.ReadFull(connection, greeting)
		_, _ = connection.Write([]byte{5, 0})
		head := make([]byte, 5)
		_, _ = io.ReadFull(connection, head)
		request := make([]byte, int(head[4])+2)
		_, _ = io.ReadFull(connection, request)
		_, _ = connection.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
	}()
	if err := checkSOCKS5Proxy(listener.Addr().String()); err == nil {
		t.Fatal("expected rejected SOCKS5 CONNECT")
	}
}
