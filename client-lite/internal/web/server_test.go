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
	request := upsertRequest{
		Profile:         model.Profile{Name: "测试 WS", Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: 1080, WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel"}},
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
	configData, err := os.ReadFile(filepath.Join(filepath.Dir(svc.ConfigPath()), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configData), "secret-value") {
		t.Fatal("secret leaked into config")
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
