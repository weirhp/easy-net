package clashsub

import (
	"net"
	"os"
	"path/filepath"
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

func TestCleanupStaleTestDirs(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "clash-test")
	oldDir := filepath.Join(root, "old")
	recentDir := filepath.Join(root, "recent")
	for _, path := range []string{oldDir, recentDir} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "mihomo.log"), []byte("log"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldDir, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	cleanupStaleTestDirs(dir, time.Now().Add(-24*time.Hour))
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("stale directory was not removed: %v", err)
	}
	if _, err := os.Stat(recentDir); err != nil {
		t.Fatalf("recent directory was removed: %v", err)
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
