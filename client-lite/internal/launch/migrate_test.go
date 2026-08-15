package launch

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"

	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/service"
)

func TestParseHistoryMatchesListenAddress(t *testing.T) {
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := service.New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	profile := model.Profile{
		ID: "ws-1", Name: "公司代理", Type: model.ProxyTypeWebSocket,
		ListenHost: "127.0.0.1", ListenPort: 1082,
		WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel"},
	}
	if err := svc.Upsert(profile, service.SecretValues{WebSocketSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	text := "chatgpt\tChatGPT\t\t\t127.0.0.1:1082\t\t2024-01-01\t0\t\t0\told-chatgpt\t\n" +
		"hook\t通用\tC:\\\\app.exe\t--flag\t10.0.0.1:1080\t1.1.1.1\t2024-01-02\t0\t\t0\told-hook\t\n" +
		"wechat-windivert\t微信\t\t\t127.0.0.1:1082\t\t2024-01-03\t0\tauto\t1\told-wechat\t\n"
	entries := parseHistoryTSV(text, svc)
	if len(entries) != 3 {
		t.Fatalf("got %d entries: %#v", len(entries), entries)
	}
	if entries[0].ProfileID != "ws-1" || entries[0].Mode != model.LaunchModeChatGPT || entries[0].ID != "old-chatgpt" {
		t.Fatalf("chatgpt entry: %#v", entries[0])
	}
	if entries[1].ProfileID != "" || entries[1].Proxy != "10.0.0.1:1080" || entries[1].Path != `C:\app.exe` {
		t.Fatalf("unmatched hook should become a manual proxy entry: %#v", entries[1])
	}
	if !entries[2].WeChatExisting || entries[2].Mode != model.LaunchModeWeChatWinDivert || entries[2].ProfileID != "ws-1" {
		t.Fatalf("wechat entry: %#v", entries[2])
	}
}

func TestMigrateFromUTF16HistoryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "launcher-entries.tsv")
	text := "cursor\tCursor\t\t\t127.0.0.1:1088\t\t2024-01-01\t1\t\t0\tcursor-1\t\n"
	encoded := utf16.Encode([]rune(text))
	data := make([]byte, 2+len(encoded)*2)
	data[0], data[1] = 0xff, 0xfe
	for i, unit := range encoded {
		binary.LittleEndian.PutUint16(data[2+i*2:], unit)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	secrets := &memorySecrets{values: map[string]string{}}
	svc, err := service.New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), secrets)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Upsert(model.Profile{
		ID: "cursor-proxy", Name: "Cursor 代理", Type: model.ProxyTypeWebSocket,
		ListenHost: "127.0.0.1", ListenPort: 1088,
		WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel"},
	}, service.SecretValues{WebSocketSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	entries, err := migrateFromHistory(svc, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Mode != model.LaunchModeCursor || !entries[0].Isolated || entries[0].ProfileID != "cursor-proxy" {
		t.Fatalf("unexpected migrated entries: %#v", entries)
	}
}

type memorySecrets struct {
	values map[string]string
}

func (m *memorySecrets) Get(ref string) (string, error) {
	value, ok := m.values[ref]
	if !ok {
		return "", os.ErrNotExist
	}
	return value, nil
}
func (m *memorySecrets) Set(ref, value string) error {
	m.values[ref] = value
	return nil
}
func (m *memorySecrets) Delete(ref string) error {
	delete(m.values, ref)
	return nil
}
