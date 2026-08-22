package clashsub

import (
	"fmt"
	"sync"
	"testing"

	"easy-net/client-lite/internal/model"
)

type fakeRunner struct {
	mu            sync.Mutex
	running       map[string]map[string]any
	starts        int
	bypassPrivate bool
	bypassChina   bool
}

func (r *fakeRunner) Start(subscriptionID string, listenPort int, proxy map[string]any, bypassPrivate, bypassChina bool) error {
	if listenPort < 1 {
		return fmt.Errorf("invalid port")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == nil {
		r.running = map[string]map[string]any{}
	}
	r.running[subscriptionID] = proxy
	r.starts++
	r.bypassPrivate = bypassPrivate
	r.bypassChina = bypassChina
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
	sub, err := manager.Import("机场 A", "https://example.com/clash.yaml", 17890, model.DefaultClashRefreshMinutes, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Name != "机场 A" || len(sub.Nodes) != 2 || ProfileID(sub.ID) != "clash-"+sub.ID || sub.RefreshMinutes != model.DefaultClashRefreshMinutes || !sub.BypassPrivate || !sub.BypassChina {
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
	if _, err := manager.Import("机场 A", "https://example.com/other.yaml", 17891, 0, true, true); err == nil {
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

func TestSetRefreshInterval(t *testing.T) {
	manager, err := New(t.TempDir(), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	manager.SetFetcher(func(string) ([]byte, error) { return []byte(sampleYAML), nil })
	sub, err := manager.Import("机场 B", "https://example.com/clash.yaml", 17890, 0, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if sub.RefreshMinutes != 0 {
		t.Fatalf("expected never, got %d", sub.RefreshMinutes)
	}
	updated, err := manager.SetRefreshInterval(sub.ID, 180)
	if err != nil || updated.RefreshMinutes != 180 {
		t.Fatalf("interval: %#v %v", updated, err)
	}
	if _, err := manager.SetRefreshInterval("missing", 60); err == nil {
		t.Fatal("expected missing subscription")
	}
}

func TestSetBypassRestartsRunningNode(t *testing.T) {
	runner := &fakeRunner{}
	manager, err := New(t.TempDir(), runner)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetFetcher(func(string) ([]byte, error) { return []byte(sampleYAML), nil })
	sub, err := manager.Import("机场 C", "https://example.com/clash.yaml", 17890, 60, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.StartNode(sub.ID, "日本 2"); err != nil {
		t.Fatal(err)
	}
	updated, err := manager.SetBypass(sub.ID, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if updated.BypassPrivate || !updated.BypassChina {
		t.Fatalf("unexpected bypass settings: %#v", updated)
	}
	runner.mu.Lock()
	starts, bypassPrivate, bypassChina := runner.starts, runner.bypassPrivate, runner.bypassChina
	runner.mu.Unlock()
	if starts != 2 || bypassPrivate || !bypassChina {
		t.Fatalf("running node was not restarted with new rules: starts=%d private=%v china=%v", starts, bypassPrivate, bypassChina)
	}
}
