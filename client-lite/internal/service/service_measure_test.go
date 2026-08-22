package service

import (
	"net"
	"path/filepath"
	"strings"
	"testing"

	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/model"
)

func TestTestProfileDelayAndStoppedSpeed(t *testing.T) {
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
	svc, err := New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), &memorySecrets{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	profile := model.Profile{
		ID: "ext-1", Name: "v2rayN", Type: model.ProxyTypeExternal,
		ListenHost: "127.0.0.1", ListenPort: port,
	}
	if err := svc.Upsert(profile, SecretValues{}); err != nil {
		t.Fatal(err)
	}
	results, err := svc.TestProfileDelay("ext-1")
	if err != nil || len(results) != 1 || results[0].Error != "" || results[0].DelayMs < 1 {
		t.Fatalf("delay: %#v %v", results, err)
	}

	ws := model.Profile{
		ID: "ws-1", Name: "公司代理", Type: model.ProxyTypeWebSocket,
		ListenHost: "127.0.0.1", ListenPort: 18080,
		WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel"},
	}
	if err := svc.Upsert(ws, SecretValues{WebSocketSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TestProfileSpeed("ws-1"); err == nil || !strings.Contains(err.Error(), "请先启动") {
		t.Fatalf("expected start required, got %v", err)
	}
	if _, err := svc.TestProfileDelay("missing"); err == nil {
		t.Fatal("expected missing profile")
	}
}
