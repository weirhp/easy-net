package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/service"
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
