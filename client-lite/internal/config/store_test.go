package config

import (
	"os"
	"path/filepath"
	"testing"

	"easy-net/client-lite/internal/model"
)

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	store := NewStoreAt(path)
	cfg := &model.Config{Profiles: []model.Profile{{
		ID: "ws-1", Name: "测试", Type: model.ProxyTypeWebSocket,
		ListenHost: "127.0.0.1", ListenPort: 1080,
		WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel", SecretRef: "ws-1/websocket"},
	}}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Profiles) != 1 || loaded.Profiles[0].Name != "测试" {
		t.Fatalf("unexpected config: %#v", loaded)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" {
		t.Fatal("config file is empty")
	}
}

func TestLoadRejectsNonLoopbackListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"profiles":[{"id":"x","name":"x","type":"websocket","listenHost":"0.0.0.0","listenPort":1080,"websocket":{"url":"wss://example.com","secretRef":"x/websocket"}}]}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStoreAt(path).Load(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestManagedPrivateKeyLifecycleDoesNotDeleteExternalFiles(t *testing.T) {
	root := t.TempDir()
	store := NewStoreAt(filepath.Join(root, "config", "config.json"))
	managed, err := store.SavePrivateKey([]byte("private-key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteManagedPrivateKey(managed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Fatalf("managed key still exists: %v", err)
	}

	external := filepath.Join(root, "external.pem")
	if err := os.WriteFile(external, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteManagedPrivateKey(external); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(external); err != nil {
		t.Fatalf("external key was removed: %v", err)
	}
}
