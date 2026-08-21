package clashsub

import (
	"net"
	"testing"
	"time"
)

func TestTCPDelayLocalListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = conn.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	delay, err := tcpDelay("127.0.0.1", port, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if delay < 1 {
		t.Fatalf("expected positive delay, got %d", delay)
	}
}

func TestTCPDelayTimeout(t *testing.T) {
	_, err := tcpDelay("127.0.0.1", 1, 80*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout")
	}
}

func TestNodesForTestMissingSubscription(t *testing.T) {
	manager, err := New(t.TempDir(), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.nodesForTest("missing", ""); err == nil {
		t.Fatal("expected missing subscription error")
	}
}
