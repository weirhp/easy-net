package service

import (
	"path/filepath"
	"sync"
	"testing"

	"easy-net/client-lite/internal/clashsub"
	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/model"
)

type countingClashRunner struct {
	mu      sync.Mutex
	running map[string]bool
	starts  int
}

func (r *countingClashRunner) Start(id string, _ int, _ map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == nil {
		r.running = make(map[string]bool)
	}
	r.running[id] = true
	r.starts++
	return nil
}

func (r *countingClashRunner) Stop(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.running, id)
	return nil
}

func (r *countingClashRunner) Running(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running[id]
}

func (r *countingClashRunner) startCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts
}

func TestRefreshClashKeepsUnchangedRunningNode(t *testing.T) {
	const unchanged = "proxies:\n  - name: node-a\n    type: ss\n    server: 198.51.100.10\n    port: 443\n    cipher: aes-128-gcm\n    password: secret\n"
	const changed = "proxies:\n  - name: node-a\n    type: ss\n    server: 198.51.100.11\n    port: 443\n    cipher: aes-128-gcm\n    password: secret\n"

	runner := &countingClashRunner{}
	manager, err := clashsub.New(t.TempDir(), runner)
	if err != nil {
		t.Fatal(err)
	}
	content := unchanged
	manager.SetFetcher(func(string) ([]byte, error) { return []byte(content), nil })
	sub, err := manager.Import("subscription", "https://example.com/subscription.yaml", 17890, model.DefaultClashRefreshMinutes)
	if err != nil {
		t.Fatal(err)
	}

	svc, err := New(config.NewStoreAt(filepath.Join(t.TempDir(), "config.json")), &memorySecrets{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	svc.AttachClash(manager)
	if err := svc.upsertClashProfile(sub); err != nil {
		t.Fatal(err)
	}
	if err := svc.StartClashNode(sub.ID, "node-a"); err != nil {
		t.Fatal(err)
	}
	if got := runner.startCount(); got != 1 {
		t.Fatalf("initial starts = %d, want 1", got)
	}

	if _, err := svc.RefreshClash(sub.ID); err != nil {
		t.Fatal(err)
	}
	if got := runner.startCount(); got != 1 {
		t.Fatalf("unchanged refresh restarted the node: starts = %d", got)
	}

	content = changed
	if _, err := svc.RefreshClash(sub.ID); err != nil {
		t.Fatal(err)
	}
	if got := runner.startCount(); got != 2 {
		t.Fatalf("changed refresh did not restart the node: starts = %d, want 2", got)
	}
}
