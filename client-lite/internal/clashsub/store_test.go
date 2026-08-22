package clashsub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreMigratesLegacyBypassDefaults(t *testing.T) {
	dir := t.TempDir()
	legacy := `{
  "version": 2,
  "subscriptions": [{
    "id": "legacy", "name": "旧订阅", "url": "https://example.com/sub",
    "listenPort": 17890, "refreshMinutes": 60, "nodes": []
  }]
}`
	if err := os.WriteFile(filepath.Join(dir, "subscriptions.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := newStore(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Subscriptions) != 1 || !file.Subscriptions[0].BypassPrivate || !file.Subscriptions[0].BypassChina {
		t.Fatalf("legacy subscription did not receive safe defaults: %#v", file.Subscriptions)
	}
}

func TestStoreKeepsCurrentBypassSettings(t *testing.T) {
	dir := t.TempDir()
	current := `{
  "version": 3,
  "subscriptions": [{
    "id": "current", "name": "新订阅", "url": "https://example.com/sub",
    "listenPort": 17890, "refreshMinutes": 60,
    "bypassPrivate": false, "bypassChina": false, "nodes": []
  }]
}`
	if err := os.WriteFile(filepath.Join(dir, "subscriptions.json"), []byte(current), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := newStore(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Subscriptions) != 1 || file.Subscriptions[0].BypassPrivate || file.Subscriptions[0].BypassChina {
		t.Fatalf("current subscription settings changed unexpectedly: %#v", file.Subscriptions)
	}
}
