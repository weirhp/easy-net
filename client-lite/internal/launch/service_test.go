package launch

import (
	"errors"
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
	checkErr  error
	running   bool
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

func (f *fakeRunner) IsRunning(model.LaunchEntry) (bool, error) { return f.running, nil }

func (f *fakeRunner) CheckProxy(string) error { return f.checkErr }

func (f *fakeRunner) Processes() ([]ProcessInfo, error) { return nil, nil }

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

func TestApplyTakeoverRulesDoesNotRequireRunningApplication(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{running: false}
	launches, err := New(dir, testService(t, dir), runner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = launches.Upsert(model.LaunchEntry{
		Name: "运行中的应用", Mode: model.LaunchModeWinDivert, Proxy: "127.0.0.1:1082",
		Path: `D:\App\app.exe`, ProcessNames: "app.exe", AttachExisting: true, UDPMode: "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := launches.ApplySharedRules(); err != nil {
		t.Fatalf("future process rules should apply without a running target: %v", err)
	}
	if got := runner.lastArgs(); !containsArgument(got, "--windivert-existing") {
		t.Fatalf("shared takeover did not start: %#v", got)
	}
}

func TestAttachExistingReportsWinDivertPermissionFailure(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{
		running: true,
		startErr: &HookStartError{
			ExitCode: 5, Diagnostics: "The shared WinDivert engine did not become ready.",
		},
	}
	launches, err := New(dir, testService(t, dir), runner)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := launches.Upsert(model.LaunchEntry{
		Name: "运行中的应用", Mode: model.LaunchModeWinDivert, Proxy: "127.0.0.1:1082",
		Path: `D:\App\app.exe`, ProcessNames: "app.exe", AttachExisting: true, UDPMode: "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = saved
	if err := launches.ApplySharedRules(); err == nil {
		t.Fatal("expected a WinDivert startup error")
	} else {
		var permission *WinDivertStartError
		if !errors.As(err, &permission) {
			t.Fatalf("unexpected error: %v", err)
		}
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

func TestShortcutInheritsExternalDefaultProxy(t *testing.T) {
	dir := t.TempDir()
	proxies := testService(t, dir)
	if err := proxies.Upsert(model.Profile{
		ID: "clash", Name: "Clash", Type: model.ProxyTypeExternal,
		ListenHost: "127.0.0.1", ListenPort: 7890, Default: true,
	}, service.SecretValues{}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	launches, err := New(dir, proxies, runner)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := launches.Upsert(model.LaunchEntry{Name: "ChatGPT.exe", Mode: model.LaunchModeChatGPT, AttachExisting: true, ProcessNames: "ChatGPT.exe"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := launches.Start(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !view.UsesDefault || view.ListenAddress != "127.0.0.1:7890" || !containsArgument(runner.lastArgs(), "127.0.0.1:7890") {
		t.Fatalf("default proxy was not inherited: view=%#v args=%#v", view, runner.lastArgs())
	}
}

func TestStartWithManualProxyDoesNotStartLiteProfile(t *testing.T) {
	dir := t.TempDir()
	svc := testService(t, dir)
	runner := &fakeRunner{}
	launches, err := New(dir, svc, runner)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := launches.Upsert(model.LaunchEntry{
		Name: "Manual", Mode: model.LaunchModeHook, Proxy: "127.0.0.1:10808",
		Path: `D:\app.exe`,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := launches.Start(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !view.ExternalProxy || view.ListenAddress != "127.0.0.1:10808" {
		t.Fatalf("unexpected manual proxy view: %#v", view)
	}
	if got := runner.lastArgs(); len(got) < 2 || got[1] != "127.0.0.1:10808" {
		t.Fatalf("unexpected hook args: %#v", got)
	}
}

func TestStartRequiresConfirmationWhenApplicationIsRunning(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{running: true}
	launches, err := New(dir, testService(t, dir), runner)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := launches.Upsert(model.LaunchEntry{
		Name: "Cursor", Mode: model.LaunchModeCursor, Proxy: "127.0.0.1:1082",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = launches.Start(saved.ID)
	var running *AlreadyRunningError
	if !errors.As(err, &running) || running.Entry.ID != saved.ID {
		t.Fatalf("expected AlreadyRunningError, got %v", err)
	}
	if runner.lastArgs() != nil {
		t.Fatal("hook started before duplicate launch was confirmed")
	}
	if _, err := launches.StartWithOptions(saved.ID, StartOptions{ConfirmRunning: true}); err != nil {
		t.Fatal(err)
	}
	if runner.lastArgs() == nil {
		t.Fatal("hook did not start after confirmation")
	}
}

func TestStartAbortsWhenProxyPreflightFails(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{checkErr: errors.New("SOCKS5 测试失败")}
	launches, err := New(dir, testService(t, dir), runner)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := launches.Upsert(model.LaunchEntry{
		Name: "应用", Mode: model.LaunchModeHook, Proxy: "127.0.0.1:1082", Path: `D:\app.exe`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := launches.Start(saved.ID); err == nil || !strings.Contains(err.Error(), "已中止启动") {
		t.Fatalf("expected proxy preflight error, got %v", err)
	}
	if runner.lastArgs() != nil {
		t.Fatal("hook started after proxy preflight failed")
	}
}

func TestStartWinDivertUsesLiteSharedProfile(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{}
	launches, err := New(dir, testService(t, dir), runner)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := launches.Upsert(model.LaunchEntry{
		Name: "共享应用", Mode: model.LaunchModeWinDivert, Proxy: "127.0.0.1:1082",
		Path: `D:\Apps\shared.exe`, UDPMode: "proxy",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = saved
	if err := launches.ApplySharedRules(); err != nil {
		t.Fatal(err)
	}
	args := runner.lastArgs()
	joined := strings.Join(args, "\n")
	if !strings.Contains(joined, "--windivert-shared-profile") ||
		!strings.Contains(joined, filepath.Join(dir, "shared-windivert.pbprofile")) ||
		!strings.Contains(joined, "--windivert-shared-root") {
		t.Fatalf("shared WinDivert arguments missing: %#v", args)
	}
	separator := -1
	sharedOption := -1
	for index, argument := range args {
		if argument == "--" {
			separator = index
		}
		if argument == "--windivert-shared-profile" {
			sharedOption = index
		}
	}
	if separator >= 0 || sharedOption < 0 {
		t.Fatalf("shared apply should not launch a target command: %#v", args)
	}
}

func containsArgument(args []string, wanted string) bool {
	for _, argument := range args {
		if argument == wanted {
			return true
		}
	}
	return false
}
