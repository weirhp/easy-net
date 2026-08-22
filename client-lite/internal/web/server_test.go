package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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

	"easy-net/client-lite/internal/clashsub"
	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/launch"
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
	if !strings.Contains(string(page), `data-tab="apps"`) || !strings.Contains(string(page), "添加被代理应用") {
		t.Fatal("management page is missing the apps tab markup")
	}
	for _, marker := range []string{`id="batch-export"`, `id="select-all-profiles"`, `id="test-profile"`, `id="process-filter"`, `data-process-launch`, "粘贴一个或多个分享码", `data-import-clash`, `id="source-tabs"`, "导入 Clash 订阅"} {
		if !strings.Contains(string(page), marker) {
			t.Fatalf("management page is missing redesigned control %q", marker)
		}
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

	delayUnauthorized, err := http.Post(server.URL+"/api/profiles/x/delay", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = delayUnauthorized.Body.Close()
	if delayUnauthorized.StatusCode != http.StatusForbidden {
		t.Fatalf("expected profile delay 403, got %d", delayUnauthorized.StatusCode)
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

func TestBatchExportAndMultiCodeImport(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := service.New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	profiles := []model.Profile{
		{ID: "batch-a", Name: "批量 A", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1180, WebSocket: &model.WebSocketConfig{URL: "wss://a.example/tunnel"}},
		{ID: "batch-b", Name: "批量 B", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1181, WebSocket: &model.WebSocketConfig{URL: "wss://b.example/tunnel"}},
	}
	for _, profile := range profiles {
		if err := svc.Upsert(profile, service.SecretValues{WebSocketSecret: "secret-" + profile.ID}); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := New(svc, func() {})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	state := getState(t, server.URL)

	exportBody, _ := json.Marshal(map[string]any{"ids": []string{"batch-a", "batch-b", "batch-a"}})
	exportRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/export", bytes.NewReader(exportBody))
	exportRequest.Header.Set("Content-Type", "application/json")
	exportRequest.Header.Set("X-Easy-Net-Token", state.Token)
	exportResponse, err := http.DefaultClient.Do(exportRequest)
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		ShareCode  string   `json:"shareCode"`
		ShareCodes []string `json:"shareCodes"`
		Exported   int      `json:"exported"`
	}
	if err := json.NewDecoder(exportResponse.Body).Decode(&exported); err != nil {
		t.Fatal(err)
	}
	_ = exportResponse.Body.Close()
	if exportResponse.StatusCode != http.StatusOK || exported.Exported != 2 || len(exported.ShareCodes) != 2 || strings.Count(exported.ShareCode, "\n") != 1 {
		t.Fatalf("unexpected batch export: status=%d body=%#v", exportResponse.StatusCode, exported)
	}

	importValue := exported.ShareCodes[0] + "\n" + exported.ShareCodes[1] + "\n" + exported.ShareCodes[0]
	importBody, _ := json.Marshal(map[string]string{"shareCode": importValue})
	importRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/import", bytes.NewReader(importBody))
	importRequest.Header.Set("Content-Type", "application/json")
	importRequest.Header.Set("X-Easy-Net-Token", state.Token)
	importResponse, err := http.DefaultClient.Do(importRequest)
	if err != nil {
		t.Fatal(err)
	}
	var imported struct {
		IDs      []string `json:"ids"`
		Imported int      `json:"imported"`
	}
	if err := json.NewDecoder(importResponse.Body).Decode(&imported); err != nil {
		t.Fatal(err)
	}
	_ = importResponse.Body.Close()
	if importResponse.StatusCode != http.StatusOK || imported.Imported != 2 || len(imported.IDs) != 2 {
		t.Fatalf("unexpected multi import: status=%d body=%#v", importResponse.StatusCode, imported)
	}
	state = getState(t, server.URL)
	if len(state.Profiles) != 4 {
		t.Fatalf("multi import should create two profiles, got %d", len(state.Profiles))
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

func TestProbeRejectsAnotherApplication(t *testing.T) {
	other := httptest.NewServer(securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ping" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"application": "another-application", "version": "test"})
	})))
	defer other.Close()

	if _, ok := probeServerAt(other.URL); ok {
		t.Fatal("another local application was mistaken for Easy-Net Lite")
	}
}

func TestStatusFileDoesNotPersistManagementToken(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := service.New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(t.TempDir(), "lite", "status.json")
	manager, err := NewWithOptions(svc, func() {}, Options{
		ListenAddress: "127.0.0.1:0", StatusFile: statusPath,
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
		t.Fatal("management token must not be persisted in the Lite status file")
	}
	if status["application"] != "easy-net-lite" || status["control"] != manager.URL() {
		t.Fatalf("unexpected status file: %#v", status)
	}
}

func TestStatusFileFindsLiteOnFallbackPort(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "status.json")
	firstService, err := service.New(config.NewStoreAt(filepath.Join(dir, "first.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewWithOptions(firstService, func() {}, Options{
		ListenAddress: "127.0.0.1:0", StatusFile: statusPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Shutdown(t.Context()) }()

	secondService, err := service.New(config.NewStoreAt(filepath.Join(dir, "second.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewWithOptions(secondService, func() {}, Options{
		ListenAddress: "127.0.0.1:0", StatusFile: statusPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = second.Start()
	var alreadyRunning *AlreadyRunningError
	if !errors.As(err, &alreadyRunning) || alreadyRunning.URL != first.URL() {
		t.Fatalf("expected existing fallback Lite at %s, got %v", first.URL(), err)
	}
}

func TestImportReportsAutoStartFailure(t *testing.T) {
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

	manager, err := NewWithOptions(svc, func() {}, Options{})
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

func TestImportStartsLocalProxy(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := service.New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewWithOptions(svc, func() {}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()

	ping, err := http.Get(server.URL + "/api/ping")
	if err != nil {
		t.Fatal(err)
	}
	var pingBody map[string]string
	if err := json.NewDecoder(ping.Body).Decode(&pingBody); err != nil {
		t.Fatal(err)
	}
	_ = ping.Body.Close()
	if ping.StatusCode != http.StatusOK || pingBody["application"] != "easy-net-lite" {
		t.Fatalf("unexpected Lite ping: %d %#v", ping.StatusCode, pingBody)
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

type recordingHookRunner struct {
	mu       sync.Mutex
	args     [][]string
	running  bool
	startErr error
}

func (r *recordingHookRunner) Start(args []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.args = append(r.args, append([]string(nil), args...))
	return r.startErr
}
func (r *recordingHookRunner) IsRunning(model.LaunchEntry) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running, nil
}
func (r *recordingHookRunner) CheckProxy(string) error                  { return nil }
func (r *recordingHookRunner) Processes() ([]launch.ProcessInfo, error) { return nil, nil }
func (r *recordingHookRunner) Executable() (string, error)              { return "easy-net-hook.exe", nil }
func (r *recordingHookRunner) CreateShortcut(options launch.ShortcutOptions) (string, error) {
	return `C:\Users\test\Desktop\` + options.Name + `.lnk`, nil
}

func TestLaunchAPIStartsHookAfterProxy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	_ = listener.Close()
	port, _ := strconv.Atoi(portText)

	dir := t.TempDir()
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := service.New(config.NewStoreAt(filepath.Join(dir, "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Upsert(model.Profile{
		ID: "p1", Name: "应用代理", Type: model.ProxyTypeWebSocket,
		ListenHost: "127.0.0.1", ListenPort: port,
		WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel"},
	}, service.SecretValues{WebSocketSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	runner := &recordingHookRunner{}
	launches, err := launch.New(dir, svc, runner)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewWithOptions(svc, func() {}, Options{Launches: launches})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	state := getState(t, server.URL)
	if state.Features.AppLaunches && !launch.Supported() {
		t.Fatal("app launches should stay disabled on non-Windows")
	}

	body, _ := json.Marshal(model.LaunchEntry{Name: "ChatGPT", Mode: model.LaunchModeChatGPT, ProfileID: "p1"})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/launches", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Easy-Net-Token", state.Token)
	saveResponse, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		OK    bool              `json:"ok"`
		Entry model.LaunchEntry `json:"entry"`
	}
	if err := json.NewDecoder(saveResponse.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	_ = saveResponse.Body.Close()
	if saveResponse.StatusCode != http.StatusOK || !saved.OK || saved.Entry.ID == "" {
		t.Fatalf("save launch: %d %#v", saveResponse.StatusCode, saved)
	}

	startRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/launches/"+saved.Entry.ID+"/start", bytes.NewReader([]byte("{}")))
	startRequest.Header.Set("Content-Type", "application/json")
	startRequest.Header.Set("X-Easy-Net-Token", state.Token)
	startResponse, err := http.DefaultClient.Do(startRequest)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(startResponse.Body)
	_ = startResponse.Body.Close()
	if startResponse.StatusCode != http.StatusOK {
		t.Fatalf("start launch failed: %d %s", startResponse.StatusCode, payload)
	}
	defer svc.Stop("p1")
	if len(runner.args) != 1 || strings.Join(runner.args[0], " ") != "--proxy 127.0.0.1:"+portText+" --detach --gui-worker --chatgpt-app" {
		t.Fatalf("unexpected hook args: %#v", runner.args)
	}

	runner.running = true
	repeatRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/launches/"+saved.Entry.ID+"/start", bytes.NewReader([]byte(`{"confirmRunning":false}`)))
	repeatRequest.Header.Set("Content-Type", "application/json")
	repeatRequest.Header.Set("X-Easy-Net-Token", state.Token)
	repeatResponse, err := http.DefaultClient.Do(repeatRequest)
	if err != nil {
		t.Fatal(err)
	}
	var repeatPayload map[string]any
	_ = json.NewDecoder(repeatResponse.Body).Decode(&repeatPayload)
	_ = repeatResponse.Body.Close()
	if repeatResponse.StatusCode != http.StatusConflict || repeatPayload["code"] != "application_already_running" {
		t.Fatalf("repeat launch should require confirmation: %d %#v", repeatResponse.StatusCode, repeatPayload)
	}

	confirmRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/launches/"+saved.Entry.ID+"/start", bytes.NewReader([]byte(`{"confirmRunning":true}`)))
	confirmRequest.Header.Set("Content-Type", "application/json")
	confirmRequest.Header.Set("X-Easy-Net-Token", state.Token)
	confirmResponse, err := http.DefaultClient.Do(confirmRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = confirmResponse.Body.Close()
	if confirmResponse.StatusCode != http.StatusOK || len(runner.args) != 2 {
		t.Fatalf("confirmed repeat launch failed: %d %#v", confirmResponse.StatusCode, runner.args)
	}

	missingRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/launches/missing-id/start", bytes.NewReader([]byte("{}")))
	missingRequest.Header.Set("Content-Type", "application/json")
	missingRequest.Header.Set("X-Easy-Net-Token", state.Token)
	missingResponse, err := http.DefaultClient.Do(missingRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = missingResponse.Body.Close()
	if missingResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("missing launch should be 404, got %d", missingResponse.StatusCode)
	}

	shortcutRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/launches/"+saved.Entry.ID+"/shortcut", nil)
	shortcutRequest.Header.Set("X-Easy-Net-Token", state.Token)
	shortcutResponse, err := http.DefaultClient.Do(shortcutRequest)
	if err != nil {
		t.Fatal(err)
	}
	var shortcut map[string]any
	if err := json.NewDecoder(shortcutResponse.Body).Decode(&shortcut); err != nil {
		t.Fatal(err)
	}
	_ = shortcutResponse.Body.Close()
	if shortcutResponse.StatusCode != http.StatusOK || shortcut["path"] == nil {
		t.Fatalf("shortcut: %d %#v", shortcutResponse.StatusCode, shortcut)
	}

	deleteRequest, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/launches/"+saved.Entry.ID, nil)
	deleteRequest.Header.Set("X-Easy-Net-Token", state.Token)
	deleteResponse, err := http.DefaultClient.Do(deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusOK {
		t.Fatalf("delete launch: %d", deleteResponse.StatusCode)
	}
}

func TestLaunchAPIReportsSavedRuleApplyFailure(t *testing.T) {
	dir := t.TempDir()
	svc, err := service.New(config.NewStoreAt(filepath.Join(dir, "config.json")), &memorySecrets{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingHookRunner{
		running: true,
		startErr: &launch.HookStartError{
			ExitCode: 5, Diagnostics: "The shared WinDivert engine did not become ready.",
		},
	}
	launches, err := launch.New(dir, svc, runner)
	if err != nil {
		t.Fatal(err)
	}
	_ = launches.SetTakeoverEnabled(true)
	entry := model.LaunchEntry{
		Name: "接管应用", Mode: model.LaunchModeWinDivert, Proxy: "127.0.0.1:1082",
		Path: `D:\App\app.exe`, ProcessNames: "app.exe", AttachExisting: true, UDPMode: "auto",
	}
	manager, err := NewWithOptions(svc, func() {}, Options{Launches: launches})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	state := getState(t, server.URL)
	body, _ := json.Marshal(entry)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/launches", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Easy-Net-Token", state.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	applyError, _ := payload["applyError"].(string)
	if response.StatusCode != http.StatusOK || !strings.Contains(applyError, "WinDivert") {
		t.Fatalf("unexpected response: %d %#v", response.StatusCode, payload)
	}
}

func TestTakeoverAPIStoresEnabledState(t *testing.T) {
	dir := t.TempDir()
	svc, err := service.New(config.NewStoreAt(filepath.Join(dir, "config.json")), &memorySecrets{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	launches, err := launch.New(dir, svc, &recordingHookRunner{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewWithOptions(svc, func() {}, Options{Launches: launches})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	state := getState(t, server.URL)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/app-takeover", bytes.NewReader([]byte(`{"enabled":true}`)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Easy-Net-Token", state.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !launches.TakeoverEnabled() {
		t.Fatalf("takeover toggle was not stored: status=%d enabled=%v", response.StatusCode, launches.TakeoverEnabled())
	}
}

func TestApplicationTakeoverAPIStoresDisabledState(t *testing.T) {
	dir := t.TempDir()
	svc, err := service.New(config.NewStoreAt(filepath.Join(dir, "config.json")), &memorySecrets{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	launches, err := launch.New(dir, svc, &recordingHookRunner{})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := launches.Upsert(model.LaunchEntry{
		Name: "Cursor", Mode: model.LaunchModeCursor, Proxy: "127.0.0.1:1082", AttachExisting: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewWithOptions(svc, func() {}, Options{Launches: launches})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	state := getState(t, server.URL)
	request, _ := http.NewRequest(
		http.MethodPost,
		server.URL+"/api/launches/"+entry.ID+"/takeover",
		bytes.NewReader([]byte(`{"enabled":false}`)),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Easy-Net-Token", state.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	got, ok := launches.Get(entry.ID)
	if response.StatusCode != http.StatusOK || !ok || !got.TakeoverDisabled {
		t.Fatalf("application takeover toggle was not stored: status=%d entry=%#v", response.StatusCode, got)
	}
}

type testClashRunner struct {
	mu      sync.Mutex
	running map[string]bool
}

func (r *testClashRunner) Start(subscriptionID string, listenPort int, proxy map[string]any) error {
	_ = listenPort
	_ = proxy
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == nil {
		r.running = map[string]bool{}
	}
	r.running[subscriptionID] = true
	return nil
}

func (r *testClashRunner) Stop(subscriptionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.running, subscriptionID)
	return nil
}

func (r *testClashRunner) Running(subscriptionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running[subscriptionID]
}

func TestClashSubscriptionAPI(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	dir := t.TempDir()
	svc, err := service.New(config.NewStoreAt(filepath.Join(dir, "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	clashMgr, err := clashsub.New(dir, &testClashRunner{})
	if err != nil {
		t.Fatal(err)
	}
	clashMgr.SetFetcher(func(string) ([]byte, error) {
		return []byte("proxies:\n  - {name: hk-1, type: ss, server: 1.2.3.4, port: 8388, password: super-secret}\n  - {name: jp-2, type: vmess, server: jp.example.com, port: 443}\n"), nil
	})
	svc.AttachClash(clashMgr)
	manager, err := New(svc, func() {})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	state := getState(t, server.URL)

	unauthorized, err := http.Post(server.URL+"/api/subscriptions", "application/json", strings.NewReader(`{"name":"机场","url":"https://example.com/clash.yaml"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", unauthorized.StatusCode)
	}

	delayUnauthorized, err := http.Post(server.URL+"/api/subscriptions/x/delay", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = delayUnauthorized.Body.Close()
	if delayUnauthorized.StatusCode != http.StatusForbidden {
		t.Fatalf("expected delay 403, got %d", delayUnauthorized.StatusCode)
	}

	probeUnauthorized, err := http.Post(server.URL+"/api/subscriptions/x/probe", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = probeUnauthorized.Body.Close()
	if probeUnauthorized.StatusCode != http.StatusForbidden {
		t.Fatalf("expected probe 403, got %d", probeUnauthorized.StatusCode)
	}

	importReq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/subscriptions", strings.NewReader(`{"name":"机场 A","url":"https://example.com/clash.yaml"}`))
	importReq.Header.Set("Content-Type", "application/json")
	importReq.Header.Set("X-Easy-Net-Token", state.Token)
	importResp, err := http.DefaultClient.Do(importReq)
	if err != nil {
		t.Fatal(err)
	}
	var imported struct {
		OK    bool   `json:"ok"`
		ID    string `json:"id"`
		Name  string `json:"name"`
		Nodes int    `json:"nodes"`
	}
	if err := json.NewDecoder(importResp.Body).Decode(&imported); err != nil {
		t.Fatal(err)
	}
	_ = importResp.Body.Close()
	if importResp.StatusCode != http.StatusOK || !imported.OK || imported.ID == "" || imported.Nodes != 2 {
		t.Fatalf("unexpected import: %d %#v", importResp.StatusCode, imported)
	}

	state = getState(t, server.URL)
	if len(state.Subscriptions) != 1 || state.Subscriptions[0].Name != "机场 A" || len(state.Subscriptions[0].Nodes) != 2 || state.Subscriptions[0].RefreshMinutes != model.DefaultClashRefreshMinutes {
		t.Fatalf("unexpected subscriptions: %#v", state.Subscriptions)
	}
	intervalReq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/subscriptions/"+imported.ID+"/interval", strings.NewReader(`{"refreshMinutes":30}`))
	intervalReq.Header.Set("Content-Type", "application/json")
	intervalReq.Header.Set("X-Easy-Net-Token", state.Token)
	intervalResp, err := http.DefaultClient.Do(intervalReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = intervalResp.Body.Close()
	if intervalResp.StatusCode != http.StatusOK {
		t.Fatalf("interval failed: %d", intervalResp.StatusCode)
	}
	state = getState(t, server.URL)
	if state.Subscriptions[0].RefreshMinutes != 30 {
		t.Fatalf("expected refresh interval 30, got %#v", state.Subscriptions[0])
	}
	raw, _ := json.Marshal(state.Subscriptions)
	if strings.Contains(string(raw), "super-secret") || strings.Contains(strings.ToLower(string(raw)), `"raw"`) {
		t.Fatalf("subscription view leaked node secrets: %s", raw)
	}
	var clashProfile *publicProfile
	for i := range state.Profiles {
		if state.Profiles[i].Profile.Type == model.ProxyTypeClash {
			clashProfile = &state.Profiles[i].Profile
			break
		}
	}
	if clashProfile == nil || clashProfile.Clash == nil || clashProfile.Clash.SubscriptionID != imported.ID {
		t.Fatalf("missing backing clash profile: %#v", state.Profiles)
	}

	startReq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/subscriptions/"+imported.ID+"/start", strings.NewReader(`{"node":"hk-1"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startReq.Header.Set("X-Easy-Net-Token", state.Token)
	startResp, err := http.DefaultClient.Do(startReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = startResp.Body.Close()
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("start failed: %d", startResp.StatusCode)
	}

	defaultReq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/subscriptions/"+imported.ID+"/default", strings.NewReader(`{"node":"jp-2","enabled":true}`))
	defaultReq.Header.Set("Content-Type", "application/json")
	defaultReq.Header.Set("X-Easy-Net-Token", state.Token)
	defaultResp, err := http.DefaultClient.Do(defaultReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = defaultResp.Body.Close()
	if defaultResp.StatusCode != http.StatusOK {
		t.Fatalf("default failed: %d", defaultResp.StatusCode)
	}
	state = getState(t, server.URL)
	if !state.Subscriptions[0].Running || state.Subscriptions[0].SelectedNode != "jp-2" || !state.Subscriptions[0].ProfileDefault {
		t.Fatalf("expected running default node, got %#v", state.Subscriptions[0])
	}

	deleteReq, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/subscriptions/"+imported.ID, nil)
	deleteReq.Header.Set("X-Easy-Net-Token", state.Token)
	deleteResp, err := http.DefaultClient.Do(deleteReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("delete failed: %d", deleteResp.StatusCode)
	}
	state = getState(t, server.URL)
	if len(state.Subscriptions) != 0 {
		t.Fatalf("expected subscription removed, got %#v", state.Subscriptions)
	}
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
