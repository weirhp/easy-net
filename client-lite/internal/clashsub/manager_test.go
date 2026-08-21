package clashsub

import (
	"fmt"
	"sync"
	"testing"
)

type fakeRunner struct {
	mu      sync.Mutex
	running map[string]map[string]any
}

func (r *fakeRunner) Start(subscriptionID string, listenPort int, proxy map[string]any) error {
	if listenPort < 1 {
		return fmt.Errorf("invalid port")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == nil {
		r.running = map[string]map[string]any{}
	}
	r.running[subscriptionID] = proxy
	return nil
}

func (r *fakeRunner) Stop(subscriptionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.running, subscriptionID)
	return nil
}

func (r *fakeRunner) Running(subscriptionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.running[subscriptionID]
	return ok
}

func TestManagerImportStartAndRefresh(t *testing.T) {
	runner := &fakeRunner{}
	manager, err := New(t.TempDir(), runner)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetFetcher(func(string) ([]byte, error) { return []byte(sampleYAML), nil })
	sub, err := manager.Import("机场 A", "https://example.com/clash.yaml", 17890)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Name != "机场 A" || len(sub.Nodes) != 2 || ProfileID(sub.ID) != "clash-"+sub.ID {
		t.Fatalf("unexpected subscription: %#v", sub)
	}
	if err := manager.StartNode(sub.ID, "日本 2"); err != nil {
		t.Fatal(err)
	}
	if !manager.Running(sub.ID) {
		t.Fatal("expected node to be running")
	}
	got, ok := manager.Get(sub.ID)
	if !ok || got.SelectedNode != "日本 2" || !got.Active {
		t.Fatalf("selected node not persisted: %#v", got)
	}
	if err := manager.Stop(sub.ID); err != nil {
		t.Fatal(err)
	}
	stopped, _ := manager.Get(sub.ID)
	if stopped.Active || manager.Running(sub.ID) {
		t.Fatalf("manual stop must disable automatic recovery: %#v", stopped)
	}
	if err := manager.StartNode(sub.ID, "日本 2"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Import("机场 A", "https://example.com/other.yaml", 17891); err == nil {
		t.Fatal("expected duplicate tab name to fail")
	}
	refreshed, err := manager.Refresh(sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.SelectedNode != "日本 2" || len(refreshed.Nodes) != 2 {
		t.Fatalf("unexpected refresh result: %#v", refreshed)
	}
	if err := manager.Delete(sub.ID); err != nil {
		t.Fatal(err)
	}
	if manager.Running(sub.ID) {
		t.Fatal("delete should stop mihomo")
	}
}
