package service

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/sharecode"

	"github.com/gorilla/websocket"
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
	shared, err := svc.ExportShare(states[0].Profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shared.SSH == nil || shared.SSH.PrivateKey != "private-key-data" {
		t.Fatalf("private key missing from exported share payload: %#v", shared)
	}
	if err := svc.Delete(states[0].Profile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("managed key still exists: %v", err)
	}
}

func TestShareExportImportRoundTripAvoidsPortConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := New(config.NewStoreAt(path), secrets)
	if err != nil {
		t.Fatal(err)
	}
	profile := model.Profile{ID: "source", Name: "shared ws", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1080, AutoStart: true, BypassPrivate: true, WebSocket: &model.WebSocketConfig{URL: "wss://example.com"}}
	if err := svc.Upsert(profile, SecretValues{WebSocketSecret: "shared-secret"}); err != nil {
		t.Fatal(err)
	}
	payload, err := svc.ExportShare("source")
	if err != nil {
		t.Fatal(err)
	}
	code, err := sharecode.Encode(payload)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := sharecode.Decode(code)
	if err != nil {
		t.Fatal(err)
	}
	importedID, err := svc.ImportShare(decoded)
	if err != nil {
		t.Fatal(err)
	}
	imported, ok := svc.Profile(importedID)
	if !ok || imported.ListenPort != 1081 || imported.AutoStart || !imported.BypassPrivate {
		t.Fatalf("unexpected imported profile: %#v", imported)
	}
	if secrets.values[importedID+"/websocket"] != "shared-secret" {
		t.Fatal("imported websocket secret was not stored")
	}
}

func TestUpsertRollsBackSecretWhenConfigSaveFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := New(config.NewStoreAt(path), secrets)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	profile := model.Profile{ID: "rollback", Name: "rollback", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1080, WebSocket: &model.WebSocketConfig{URL: "wss://example.com"}}
	if err := svc.Upsert(profile, SecretValues{WebSocketSecret: "must-not-remain"}); err == nil {
		t.Fatal("expected config save failure")
	}
	if _, ok := secrets.values["rollback/websocket"]; ok {
		t.Fatal("secret was not rolled back")
	}
	if len(svc.States()) != 0 {
		t.Fatal("failed upsert changed in-memory config")
	}
}

func TestDeleteKeepsInMemoryConfigWhenSaveFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := New(config.NewStoreAt(path), secrets)
	if err != nil {
		t.Fatal(err)
	}
	profile := model.Profile{ID: "keep", Name: "keep", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1080, WebSocket: &model.WebSocketConfig{URL: "wss://example.com"}}
	if err := svc.Upsert(profile, SecretValues{WebSocketSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete("keep"); err == nil {
		t.Fatal("expected delete save failure")
	}
	if len(svc.States()) != 1 {
		t.Fatal("failed delete changed in-memory config")
	}
	if secrets.values["keep/websocket"] != "secret" {
		t.Fatal("failed delete removed secret")
	}
}

func TestIncomingInternalFieldsAreIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	secrets := &memorySecrets{values: map[string]string{"attacker/ref": "wrong"}}
	svc, err := New(config.NewStoreAt(path), secrets)
	if err != nil {
		t.Fatal(err)
	}
	profile := model.Profile{ID: "safe", Name: "safe", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1080, WebSocket: &model.WebSocketConfig{URL: "wss://example.com", SecretRef: "attacker/ref"}}
	if err := svc.Upsert(profile, SecretValues{WebSocketSecret: "correct"}); err != nil {
		t.Fatal(err)
	}
	stored, ok := svc.Profile("safe")
	if !ok || stored.WebSocket.SecretRef != "safe/websocket" {
		t.Fatalf("server did not own secret reference: %#v", stored)
	}
}

type blockingSecrets struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingSecrets) Get(string) (string, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return "secret", nil
}
func (b *blockingSecrets) Set(string, string) error { return nil }
func (b *blockingSecrets) Delete(string) error      { return nil }

func TestStatesRemainResponsiveWhileProfileStarts(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	_ = listener.Close()
	port, _ := strconv.Atoi(portText)
	path := filepath.Join(t.TempDir(), "config.json")
	store := config.NewStoreAt(path)
	profile := model.Profile{ID: "slow", Name: "slow", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: port, WebSocket: &model.WebSocketConfig{URL: "wss://example.com", SecretRef: "slow/websocket"}}
	if err := store.Save(&model.Config{Profiles: []model.Profile{profile}}); err != nil {
		t.Fatal(err)
	}
	secrets := &blockingSecrets{entered: make(chan struct{}), release: make(chan struct{})}
	svc, err := New(store, secrets)
	if err != nil {
		t.Fatal(err)
	}
	startDone := make(chan error, 1)
	go func() { startDone <- svc.Start("slow") }()
	select {
	case <-secrets.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("start did not reach secret store")
	}
	statesDone := make(chan []ProfileState, 1)
	go func() { statesDone <- svc.States() }()
	select {
	case states := <-statesDone:
		if len(states) != 1 || !states[0].Starting {
			t.Fatalf("unexpected states while starting: %#v", states)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("state query was blocked by profile start")
	}
	close(secrets.release)
	if err := <-startDone; err != nil {
		t.Fatal(err)
	}
	svc.Stop("slow")
}

func TestConnectionRecordsFriendlyWebSocketAuthenticationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/tunnel"

	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	profile := model.Profile{ID: "ws-auth", Name: "WS auth", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1080, WebSocket: &model.WebSocketConfig{URL: wsURL, AllowInsecure: true}}
	if err := svc.Upsert(profile, SecretValues{WebSocketSecret: "wrong-secret"}); err != nil {
		t.Fatal(err)
	}
	err = svc.TestConnection("ws-auth")
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "密钥") {
		t.Fatalf("unexpected connection test error: %v", err)
	}
	state := svc.States()[0]
	if state.ConnectionStatus != "error" || state.ConnectionAt.IsZero() || state.ConnectionError != err.Error() {
		t.Fatalf("unexpected connection state: %#v", state)
	}
}

func TestConnectionRecordsSuccessfulWebSocketProbe(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid-secret" || r.Header.Get("X-Target-Host") == "" || r.Header.Get("X-Target-Port") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/tunnel"

	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	profile := model.Profile{ID: "ws-ok", Name: "WS ok", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1080, WebSocket: &model.WebSocketConfig{URL: wsURL, AllowInsecure: true}}
	if err := svc.Upsert(profile, SecretValues{WebSocketSecret: "valid-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.TestConnection("ws-ok"); err != nil {
		t.Fatal(err)
	}
	state := svc.States()[0]
	if state.ConnectionStatus != "success" || state.ConnectionAt.IsZero() || state.ConnectionError != "" {
		t.Fatalf("unexpected connection state: %#v", state)
	}
}

func TestDialResultsUpdateConnectionHealth(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	profile := model.Profile{ID: "health", Name: "health", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1080, WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel"}}
	if err := svc.Upsert(profile, SecretValues{WebSocketSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	svc.revisions[profile.ID] = 2
	svc.mu.Unlock()
	svc.recordConnectionResult(profile.ID, 2, profile, context.DeadlineExceeded)
	state := svc.States()[0]
	if state.ConnectionStatus != "error" || !strings.Contains(state.ConnectionError, "超时") {
		t.Fatalf("unexpected failed connection health: %#v", state)
	}
	svc.recordConnectionResult(profile.ID, 2, profile, nil)
	state = svc.States()[0]
	if state.ConnectionStatus != "success" || state.ConnectionError != "" {
		t.Fatalf("unexpected successful connection health: %#v", state)
	}
}
