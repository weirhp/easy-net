package launch

import (
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/service"
)

type fakeRunner struct {
	mu        sync.Mutex
	args      [][]string
	startErr  error
	shortcuts []ShortcutOptions
}

func (f *fakeRunner) Start(args []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.args = append(f.args, append([]string(nil), args...))
	return nil
}

func (f *fakeRunner) Executable() (string, error) { return "easy-net-hook.exe", nil }

func (f *fakeRunner) CreateShortcut(options ShortcutOptions) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shortcuts = append(f.shortcuts, options)
	return `C:\Users\test\Desktop\` + options.Name + `（代理）.lnk`, nil
}

func (f *fakeRunner) lastArgs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.args) == 0 {
		return nil
	}
	return append([]string(nil), f.args[len(f.args)-1]...)
}

func testService(t *testing.T, dir string) *service.Service {
	t.Helper()
	svc, err := service.New(config.NewStoreAt(filepath.Join(dir, "config.json")), &memorySecrets{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestLaunchStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	svc := testService(t, dir)
	if err := svc.Upsert(model.Profile{
		ID: "p1", Name: "代理", Type: model.ProxyTypeWebSocket,
		ListenHost: "127.0.0.1", ListenPort: 1091,
		WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel"},
	}, service.SecretValues{WebSocketSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	launches, err := New(dir, svc, &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := launches.Upsert(model.LaunchEntry{Name: "ChatGPT", Mode: model.LaunchModeChatGPT, ProfileID: "p1"})
	if err != nil || saved.ID == "" {
		t.Fatalf("upsert: %#v %v", saved, err)
	}
	reloaded, err := New(dir, svc, &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Get(saved.ID)
	if !ok || got.Name != "ChatGPT" || got.ProfileID != "p1" {
		t.Fatalf("reload: %#v %v", got, ok)
	}
}

func TestStartDoesNotSpawnHookWhenProxyFails(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	_, portText, _ := net.SplitHostPort(occupied.Addr().String())
	port, _ := strconv.Atoi(portText)

	dir := t.TempDir()
	svc := testService(t, dir)
	if err := svc.Upsert(model.Profile{
		ID: "p1", Name: "端口占用", Type: model.ProxyTypeWebSocket,
		ListenHost: "127.0.0.1", ListenPort: port,
		WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel"},
	}, service.SecretValues{WebSocketSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	launches, err := New(dir, svc, runner)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := launches.Upsert(model.LaunchEntry{Name: "ChatGPT", Mode: model.LaunchModeChatGPT, ProfileID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := launches.Start(saved.ID); err == nil {
		t.Fatal("expected proxy start failure")
	}
	if got := runner.lastArgs(); got != nil {
		t.Fatalf("hook should not start after proxy failure: %#v", got)
	}
}

func TestStartSpawnsHookAfterLocalProxy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	_ = listener.Close()
	port, _ := strconv.Atoi(portText)

	dir := t.TempDir()
	svc := testService(t, dir)
	if err := svc.Upsert(model.Profile{
		ID: "p1", Name: "可用代理", Type: model.ProxyTypeWebSocket,
		ListenHost: "127.0.0.1", ListenPort: port,
		WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel"},
	}, service.SecretValues{WebSocketSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	launches, err := New(dir, svc, runner)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := launches.Upsert(model.LaunchEntry{Name: "ChatGPT", Mode: model.LaunchModeChatGPT, ProfileID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := launches.Start(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Stop("p1")
	if view.ListenAddress != "127.0.0.1:"+portText {
		t.Fatalf("listen address: %#v", view)
	}
	args := runner.lastArgs()
	want := []string{"--proxy", "127.0.0.1:" + portText, "--detach", "--gui-worker", "--chatgpt-app"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("hook args: %#v", args)
	}
}

func TestCreateShortcutPointsAtLiteLaunchEntry(t *testing.T) {
	dir := t.TempDir()
	svc := testService(t, dir)
	if err := svc.Upsert(model.Profile{
		ID: "p1", Name: "代理", Type: model.ProxyTypeWebSocket,
		ListenHost: "127.0.0.1", ListenPort: 1093,
		WebSocket: &model.WebSocketConfig{URL: "wss://example.com/tunnel"},
	}, service.SecretValues{WebSocketSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	launches, err := New(dir, svc, runner)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := launches.Upsert(model.LaunchEntry{Name: "Cursor", Mode: model.LaunchModeCursor, ProfileID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	path, err := launches.CreateShortcut(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, "Cursor") {
		t.Fatalf("shortcut path: %s", path)
	}
	if len(runner.shortcuts) != 1 || !strings.Contains(runner.shortcuts[0].Arguments, saved.ID) {
		t.Fatalf("shortcut options: %#v", runner.shortcuts)
	}
	if !strings.HasPrefix(runner.shortcuts[0].Arguments, "--launch-entry ") {
		t.Fatalf("shortcut should launch Lite entry: %#v", runner.shortcuts[0])
	}
	if runner.shortcuts[0].IconPath != "" || runner.shortcuts[0].UseChatGPTIcon {
		t.Fatalf("unexpected Cursor shortcut icon options: %#v", runner.shortcuts[0])
	}
}
