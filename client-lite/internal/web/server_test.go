package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/service"
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

func TestManagementPageAndProfileAPI(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := service.New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(svc, func() {})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(page), "Easy-Net Lite") {
		t.Fatalf("unexpected page response: %d", response.StatusCode)
	}
	if !strings.Contains(response.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatal("missing content security policy")
	}

	state := getState(t, server.URL)
	if state.Version == "" {
		t.Fatal("missing application version")
	}
	maliciousBody := []byte(`{"profile":{"id":"","name":"bad","type":"websocket","listenHost":"127.0.0.1","listenPort":1080,"autoStart":false,"websocket":{"url":"wss://example.com","secretRef":"attacker/ref"},"ssh":null},"websocketSecret":"secret","sshPassword":"","sshPassphrase":"","sshPrivateKey":""}`)
	maliciousRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/profiles", bytes.NewReader(maliciousBody))
	maliciousRequest.Header.Set("Content-Type", "application/json")
	maliciousRequest.Header.Set("X-Easy-Net-Token", state.Token)
	maliciousResponse, err := http.DefaultClient.Do(maliciousRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = maliciousResponse.Body.Close()
	if maliciousResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected internal field rejection, got %d", maliciousResponse.StatusCode)
	}
	request := upsertRequest{
		Profile:         profileInput{Name: "测试 WS", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1080, WebSocket: &webSocketInput{URL: "wss://example.com/tunnel"}},
		WebSocketSecret: "secret-value",
	}
	body, _ := json.Marshal(request)
	unauthorized, err := http.Post(server.URL+"/api/profiles", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", unauthorized.StatusCode)
	}

	httpRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/profiles", bytes.NewReader(body))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("X-Easy-Net-Token", state.Token)
	saveResponse, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer saveResponse.Body.Close()
	if saveResponse.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(saveResponse.Body)
		t.Fatalf("save failed: %d %s", saveResponse.StatusCode, data)
	}
	state = getState(t, server.URL)
	if len(state.Profiles) != 1 || state.Profiles[0].Profile.Name != "测试 WS" {
		t.Fatalf("unexpected state: %#v", state)
	}
	profileID := state.Profiles[0].Profile.ID
	shareRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/profiles/"+profileID+"/share", nil)
	shareRequest.Header.Set("X-Easy-Net-Token", state.Token)
	shareResponse, err := http.DefaultClient.Do(shareRequest)
	if err != nil {
		t.Fatal(err)
	}
	var shared struct {
		ShareCode string `json:"shareCode"`
	}
	if err := json.NewDecoder(shareResponse.Body).Decode(&shared); err != nil {
		t.Fatal(err)
	}
	_ = shareResponse.Body.Close()
	if shareResponse.StatusCode != http.StatusOK || !strings.HasPrefix(shared.ShareCode, "ENL1.") || strings.Contains(shared.ShareCode, "secret-value") {
		t.Fatalf("unexpected share response: status=%d code=%q", shareResponse.StatusCode, shared.ShareCode)
	}
	importBody, _ := json.Marshal(map[string]string{"shareCode": shared.ShareCode})
	importRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/import", bytes.NewReader(importBody))
	importRequest.Header.Set("Content-Type", "application/json")
	importRequest.Header.Set("X-Easy-Net-Token", state.Token)
	importResponse, err := http.DefaultClient.Do(importRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = importResponse.Body.Close()
	if importResponse.StatusCode != http.StatusOK {
		t.Fatalf("import failed: %d", importResponse.StatusCode)
	}
	state = getState(t, server.URL)
	if len(state.Profiles) != 2 || state.Profiles[0].Profile.ListenPort == state.Profiles[1].Profile.ListenPort {
		t.Fatalf("import did not avoid local port conflict: %#v", state.Profiles)
	}
	stateJSON, _ := json.Marshal(state)
	for _, internalField := range []string{"secretRef", "passwordRef", "privateKeyPath", "passphraseRef", "hostKeyFingerprint"} {
		if bytes.Contains(stateJSON, []byte(internalField)) {
			t.Fatalf("internal field %q leaked into state", internalField)
		}
	}
	configData, err := os.ReadFile(filepath.Join(filepath.Dir(svc.ConfigPath()), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configData), "secret-value") {
		t.Fatal("secret leaked into config")
	}
}

func TestRejectsDNSRebindingAndCrossOriginRequests(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := service.New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(svc, func() {})
	if err != nil {
		t.Fatal(err)
	}

	rebinding := httptest.NewRequest(http.MethodGet, "http://evil.example/api/state", nil)
	rebinding.Host = "evil.example"
	rebindingResponse := httptest.NewRecorder()
	manager.Handler().ServeHTTP(rebindingResponse, rebinding)
	if rebindingResponse.Code != http.StatusMisdirectedRequest {
		t.Fatalf("expected DNS rebinding rejection, got %d", rebindingResponse.Code)
	}

	crossOrigin := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18081/api/state", nil)
	crossOrigin.Host = "127.0.0.1:18081"
	crossOrigin.Header.Set("Origin", "https://evil.example")
	crossOriginResponse := httptest.NewRecorder()
	manager.Handler().ServeHTTP(crossOriginResponse, crossOrigin)
	if crossOriginResponse.Code != http.StatusForbidden {
		t.Fatalf("expected cross-origin rejection, got %d", crossOriginResponse.Code)
	}
}

func TestProfileConnectionTestEndpointUpdatesState(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
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
	defer remote.Close()

	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := service.New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	profile := model.Profile{ID: "test-ws", Name: "test ws", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1080, WebSocket: &model.WebSocketConfig{URL: "ws" + strings.TrimPrefix(remote.URL, "http") + "/tunnel", AllowInsecure: true}}
	if err := svc.Upsert(profile, service.SecretValues{WebSocketSecret: "test-secret"}); err != nil {
		t.Fatal(err)
	}
	manager, err := New(svc, func() {})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	state := getState(t, server.URL)

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/profiles/test-ws/test", nil)
	request.Header.Set("X-Easy-Net-Token", state.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("connection test failed: %d", response.StatusCode)
	}
	state = getState(t, server.URL)
	if len(state.Profiles) != 1 || state.Profiles[0].ConnectionStatus != "success" || state.Profiles[0].ConnectionAt == "" {
		t.Fatalf("connection state was not exposed: %#v", state.Profiles)
	}
}

func TestProbeRecognizesLegacyInstance(t *testing.T) {
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ping" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		writeJSON(w, http.StatusOK, map[string]string{
			"configPath": "C:/Users/test/config.json",
			"token":      strings.Repeat("a", 48),
			"version":    "0.1.0",
		})
	}))
	defer legacy.Close()
	if url, ok := probeServerAt(legacy.URL); !ok || url != legacy.URL {
		t.Fatalf("legacy instance was not recognized: %q %v", url, ok)
	}

	unrelated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"token": strings.Repeat("a", 48), "version": "0.1.0"})
	}))
	defer unrelated.Close()
	if _, ok := probeServerAt(unrelated.URL); ok {
		t.Fatal("unrelated local server was recognized as Easy-Net Lite")
	}
}

func TestProbeRequiresMatchingApplication(t *testing.T) {
	engine := httptest.NewServer(securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ping" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"application": "easy-net-engine", "version": "test"})
	})))
	defer engine.Close()

	if _, ok := probeApplicationServerAt(engine.URL, "easy-net-lite", true); ok {
		t.Fatal("engine was mistaken for the regular Easy-Net Lite application")
	}
	if url, ok := probeApplicationServerAt(engine.URL, "easy-net-engine", false); !ok || url != engine.URL {
		t.Fatalf("matching engine was not recognized: %q %v", url, ok)
	}
}

func TestEngineUsesFallbackPortWhenLiteOwnsPreferredPort(t *testing.T) {
	lite := httptest.NewServer(securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ping" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"application": "easy-net-lite", "version": "test"})
	})))
	defer lite.Close()
	liteAddress := strings.TrimPrefix(lite.URL, "http://")

	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := service.New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewWithOptions(svc, func() {}, Options{
		ListenAddress: liteAddress, Application: "easy-net-engine", DisableAssets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Shutdown(t.Context()) }()
	if manager.URL() == lite.URL {
		t.Fatal("engine reused the regular Lite management endpoint")
	}
	if !strings.HasPrefix(manager.URL(), "http://127.0.0.1:") {
		t.Fatalf("engine did not select a loopback fallback: %s", manager.URL())
	}
}

func TestStatusFileDoesNotPersistManagementToken(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := service.New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(t.TempDir(), "engine", "status.json")
	manager, err := NewWithOptions(svc, func() {}, Options{
		ListenAddress: "127.0.0.1:0", StatusFile: statusPath,
		Application: "easy-net-engine", DisableAssets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Shutdown(t.Context()) }()

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	if _, exists := status["token"]; exists {
		t.Fatal("management token must not be persisted in the engine status file")
	}
	if status["application"] != "easy-net-engine" || status["control"] != manager.URL() {
		t.Fatalf("unexpected status file: %#v", status)
	}
}

func TestEngineImportReportsAutoStartFailure(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := service.New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	available, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	preferredPort := available.Addr().(*net.TCPAddr).Port
	_ = available.Close()
	payload := sharecode.Payload{
		Version: sharecode.CurrentVersion, Name: "occupied", Type: model.ProxyTypeWebSocket,
		PreferredPort: preferredPort,
		WebSocket:     &sharecode.WebSocketConfig{URL: "wss://example.com/tunnel", Secret: "secret"},
	}
	id, err := svc.ImportShare(payload)
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := svc.Profile(id)
	if !ok {
		t.Fatal("imported profile is missing")
	}
	occupied, err := net.Listen("tcp", profile.ListenAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	payload.PreferredPort = profile.ListenPort

	manager, err := NewWithOptions(svc, func() {}, Options{Application: "easy-net-engine", DisableAssets: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	state := getState(t, server.URL)
	code, err := sharecode.Encode(payload)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"shareCode": code, "autoStart": true, "reuse": true})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/import", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Easy-Net-Token", state.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || result["ok"] != false || result["error"] == "" {
		t.Fatalf("auto-start failure was reported as success: status=%d body=%#v", response.StatusCode, result)
	}
}

func TestEngineImportStartsLocalProxy(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := service.New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewWithOptions(svc, func() {}, Options{
		Application:   "easy-net-engine",
		DisableAssets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()

	page, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Body.Close()
	if page.StatusCode != http.StatusNotFound {
		t.Fatalf("engine should not serve the management UI, got %d", page.StatusCode)
	}
	ping, err := http.Get(server.URL + "/api/ping")
	if err != nil {
		t.Fatal(err)
	}
	var pingBody map[string]string
	if err := json.NewDecoder(ping.Body).Decode(&pingBody); err != nil {
		t.Fatal(err)
	}
	_ = ping.Body.Close()
	if ping.StatusCode != http.StatusOK || pingBody["application"] != "easy-net-engine" {
		t.Fatalf("unexpected engine ping: %d %#v", ping.StatusCode, pingBody)
	}

	profile := model.Profile{ID: "source", Name: "hook ws", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 18090, WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel"}}
	if err := svc.Upsert(profile, service.SecretValues{WebSocketSecret: "engine-secret"}); err != nil {
		t.Fatal(err)
	}
	state := getState(t, server.URL)
	shareRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/profiles/source/share", nil)
	shareRequest.Header.Set("X-Easy-Net-Token", state.Token)
	shareResponse, err := http.DefaultClient.Do(shareRequest)
	if err != nil {
		t.Fatal(err)
	}
	var shared struct {
		ShareCode string `json:"shareCode"`
	}
	if err := json.NewDecoder(shareResponse.Body).Decode(&shared); err != nil {
		t.Fatal(err)
	}
	_ = shareResponse.Body.Close()

	importBody, _ := json.Marshal(map[string]any{"shareCode": shared.ShareCode, "autoStart": true, "reuse": true})
	importRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/import", bytes.NewReader(importBody))
	importRequest.Header.Set("Content-Type", "application/json")
	importRequest.Header.Set("X-Easy-Net-Token", state.Token)
	importResponse, err := http.DefaultClient.Do(importRequest)
	if err != nil {
		t.Fatal(err)
	}
	var imported map[string]any
	if err := json.NewDecoder(importResponse.Body).Decode(&imported); err != nil {
		t.Fatal(err)
	}
	_ = importResponse.Body.Close()
	if importResponse.StatusCode != http.StatusOK {
		t.Fatalf("engine import failed: %d %#v", importResponse.StatusCode, imported)
	}
	if imported["id"] != "source" {
		t.Fatalf("reuse should keep the existing profile, got %#v", imported)
	}
	if imported["running"] != true {
		t.Fatalf("imported proxy was not started: %#v", imported)
	}
	if imported["listenAddress"] != "127.0.0.1:18090" {
		t.Fatalf("unexpected listen address: %#v", imported)
	}
	proxyRequest, err := http.Get(server.URL + "/api/proxy?id=source")
	if err != nil {
		t.Fatal(err)
	}
	var proxy map[string]any
	if err := json.NewDecoder(proxyRequest.Body).Decode(&proxy); err != nil {
		t.Fatal(err)
	}
	_ = proxyRequest.Body.Close()
	if proxyRequest.StatusCode != http.StatusOK || proxy["running"] != true {
		t.Fatalf("proxy summary failed: %d %#v", proxyRequest.StatusCode, proxy)
	}
	svc.Stop("source")
}

func getState(t *testing.T, baseURL string) stateResponse {
	t.Helper()
	response, err := http.Get(baseURL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var state stateResponse
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Token == "" {
		t.Fatal("missing management token")
	}
	return state
}
