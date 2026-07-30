package service

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/model"
)

type memorySecrets struct {
	mu     sync.Mutex
	values map[string]string
}

func (m *memorySecrets) Get(ref string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[ref]
	if !ok {
		return "", os.ErrNotExist
	}
	return value, nil
}
func (m *memorySecrets) Set(ref, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[ref] = value
	return nil
}
func (m *memorySecrets) Delete(ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, ref)
	return nil
}

func TestUpsertStoresSecretOutsideConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := New(config.NewStoreAt(path), secrets)
	if err != nil {
		t.Fatal(err)
	}
	p := model.Profile{Name: "WS", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1080, WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel"}}
	if err := svc.Upsert(p, SecretValues{WebSocketSecret: "top-secret"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "top-secret") {
		t.Fatal("secret leaked into config")
	}
	states := svc.States()
	if len(states) != 1 || states[0].Profile.WebSocket.SecretRef == "" {
		t.Fatalf("unexpected state: %#v", states)
	}
}

func TestUpsertRejectsDuplicatePort(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		p := model.Profile{Name: name, Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1080, WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel"}}
		err = svc.Upsert(p, SecretValues{WebSocketSecret: name})
		if name == "one" && err != nil {
			t.Fatal(err)
		}
	}
	if err == nil {
		t.Fatal("expected duplicate port error")
	}
}

func TestPrivateKeyImportIsRemovedWithProfile(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	profile := model.Profile{Name: "SSH", Type: model.ProxyTypeSSH, ListenHost: "127.0.0.1", ListenPort: 1082, SSH: &model.SSHConfig{Host: "example.com", Port: 22, Username: "user", AuthType: model.AuthTypePrivateKey}}
	if err := svc.Upsert(profile, SecretValues{SSHPrivateKey: []byte("private-key-data")}); err != nil {
		t.Fatal(err)
	}
	states := svc.States()
	if len(states) != 1 {
		t.Fatalf("unexpected states: %#v", states)
	}
	keyPath := states[0].Profile.SSH.PrivateKeyPath
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(states[0].Profile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("managed key still exists: %v", err)
	}
}
