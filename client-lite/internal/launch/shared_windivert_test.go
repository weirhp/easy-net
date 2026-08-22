package launch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easy-net/client-lite/internal/model"
)

func TestSharedWinDivertProfileCombinesApplicationsAndProxies(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "launches.json"), []byte(`{"version":2,"entries":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	launches, err := New(dir, testService(t, dir), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []model.LaunchEntry{
		{Name: "应用一", Mode: model.LaunchModeWinDivert, Proxy: "127.0.0.1:1081", Path: `D:\One\one.exe`, ProcessNames: "one-helper.exe", UDPMode: "block"},
		{Name: "应用二", Mode: model.LaunchModeWinDivert, Proxy: "127.0.0.1:1082", Path: `D:\Two\two.exe`, UDPMode: "proxy"},
	} {
		if _, err := launches.Upsert(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := launches.SetTakeoverEnabled(true); err != nil {
		t.Fatal(err)
	}
	path, err := launches.writeSharedWinDivertProfile()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var profile bridgeProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatal(err)
	}
	if len(profile.ProxyConfigs) != 2 {
		t.Fatalf("proxy configs = %d, want 2", len(profile.ProxyConfigs))
	}
	var firstTCP, firstUDP, secondTCP, firstProxyBypass, secondProxyBypass bool
	for _, rule := range profile.ProxyRules {
		if strings.Contains(rule.ProcessName, "one.exe") && rule.Protocol == "TCP" && rule.Action == "PROXY" && rule.ProxyID == 1 {
			firstTCP = true
		}
		if strings.Contains(rule.ProcessName, "one-helper.exe") && rule.Protocol == "UDP" && rule.Action == "BLOCK" {
			firstUDP = true
		}
		if strings.Contains(rule.ProcessName, "two.exe") && rule.Protocol == "TCP" && rule.Action == "PROXY" && rule.ProxyID == 2 {
			secondTCP = true
		}
		if strings.Contains(rule.ProcessName, "one.exe") && rule.TargetHosts == "127.0.0.1" && rule.TargetPorts == "1081" && rule.Action == "DIRECT" {
			firstProxyBypass = true
		}
		if strings.Contains(rule.ProcessName, "two.exe") && rule.TargetHosts == "127.0.0.1" && rule.TargetPorts == "1082" && rule.Action == "DIRECT" {
			secondProxyBypass = true
		}
	}
	if !firstTCP || !firstUDP || !secondTCP || !firstProxyBypass || !secondProxyBypass {
		t.Fatalf("missing shared rules: firstTCP=%v firstUDP=%v secondTCP=%v firstProxyBypass=%v secondProxyBypass=%v", firstTCP, firstUDP, secondTCP, firstProxyBypass, secondProxyBypass)
	}
}

func TestUpsertReusesExistingApplicationByExecutable(t *testing.T) {
	dir := t.TempDir()
	launches, err := New(dir, testService(t, dir), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"第一个", "第二个"} {
		if _, err := launches.Upsert(model.LaunchEntry{
			Name: name, Mode: model.LaunchModeWinDivert, Proxy: "127.0.0.1:1082",
			Path: `D:\Apps\shared.exe`, UDPMode: "auto",
		}); err != nil {
			t.Fatal(err)
		}
	}
	matches := 0
	for _, entry := range launches.List() {
		if strings.EqualFold(entry.Path, `D:\Apps\shared.exe`) {
			matches++
			if entry.Name != "第二个" {
				t.Fatalf("existing rule was not updated: %#v", entry)
			}
		}
	}
	if matches != 1 {
		t.Fatalf("duplicate executable should produce one rule, got %d", matches)
	}
}

func TestSharedWinDivertProfileIncludesRunningBrowser(t *testing.T) {
	dir := t.TempDir()
	launches, err := New(dir, testService(t, dir), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := launches.Upsert(model.LaunchEntry{
		Name: "Chrome", Mode: model.LaunchModeChrome, Proxy: "127.0.0.1:1082",
		AttachExisting: true, UDPMode: "block",
	}); err != nil {
		t.Fatal(err)
	}
	if err := launches.SetTakeoverEnabled(true); err != nil {
		t.Fatal(err)
	}
	path, err := launches.writeSharedWinDivertProfile()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"ProcessName": "chrome.exe"`) {
		t.Fatalf("shared profile does not contain Chrome: %s", data)
	}
}

func TestDisabledApplicationIsExcludedFromSharedWinDivert(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "launches.json"), []byte(`{"version":3,"entries":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	launches, err := New(dir, testService(t, dir), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := launches.Upsert(model.LaunchEntry{
		Name: "启用应用", Mode: model.LaunchModeWinDivert, Proxy: "127.0.0.1:1082",
		Path: `D:\Enabled\enabled.exe`, AttachExisting: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := launches.Upsert(model.LaunchEntry{
		Name: "关闭应用", Mode: model.LaunchModeWinDivert, Proxy: "127.0.0.1:1083",
		Path: `D:\Disabled\disabled.exe`, AttachExisting: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := launches.SetEntryTakeover(disabled.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := launches.SetTakeoverEnabled(true); err != nil {
		t.Fatal(err)
	}
	path, err := launches.writeSharedWinDivertProfile()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(data))
	if !strings.Contains(text, "enabled.exe") || strings.Contains(text, "disabled.exe") {
		t.Fatalf("per-application takeover was not applied: %s", data)
	}
	if got, ok := launches.Get(enabled.ID); !ok || got.TakeoverDisabled {
		t.Fatalf("new applications must default to takeover enabled: %#v", got)
	}
	reloaded, err := New(dir, testService(t, dir), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reloaded.Get(disabled.ID); !ok || !got.TakeoverDisabled {
		t.Fatalf("disabled takeover setting was not persisted: %#v", got)
	}
}

func TestAllApplicationsCanDisableTakeoverWithoutAProxy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "launches.json"), []byte(`{"version":3,"entries":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	launches, err := New(dir, testService(t, dir), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := launches.Upsert(model.LaunchEntry{
		Name: "暂不接管", Mode: model.LaunchModeHook, Path: `D:\App\app.exe`, AttachExisting: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := launches.SetEntryTakeover(entry.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, ok := launches.Get(entry.ID); !ok || !got.TakeoverDisabled {
		t.Fatalf("application takeover should be disabled before enabling the global service: %#v", got)
	}
	if got := launches.sharedTakeoverEntries(); len(got) != 0 {
		t.Fatalf("disabled application still appears in shared rules: %#v", got)
	}
	if err := launches.SetTakeoverEnabled(true); err != nil {
		t.Fatalf("disabled applications must not require a proxy: %v", err)
	}
	if status := launches.TakeoverStatus(); status.State != "idle" || !status.Enabled {
		t.Fatalf("all-disabled takeover should be idle, got %#v", status)
	}
}

func TestSharedWinDivertProfileDeduplicatesSharedHelper(t *testing.T) {
	dir := t.TempDir()
	launches, err := New(dir, testService(t, dir), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []model.LaunchEntry{
		{Name: "编辑器一", Mode: model.LaunchModeWinDivert, Proxy: "127.0.0.1:1082", Path: `D:\One\one.exe`, ProcessNames: "shared-helper.exe", UDPMode: "proxy"},
		{Name: "编辑器二", Mode: model.LaunchModeWinDivert, Proxy: "127.0.0.1:1083", Path: `D:\Two\two.exe`, ProcessNames: "shared-helper.exe", UDPMode: "proxy"},
	} {
		if _, err := launches.Upsert(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := launches.SetTakeoverEnabled(true); err != nil {
		t.Fatal(err)
	}
	path, err := launches.writeSharedWinDivertProfile()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var profile bridgeProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatal(err)
	}
	for _, rule := range profile.ProxyRules {
		if strings.Count(strings.ToLower(rule.ProcessName), "shared-helper.exe") > 1 {
			t.Fatalf("shared helper was duplicated in one rule: %#v", rule)
		}
	}
	if !strings.Contains(strings.ToLower(string(data)), "shared-helper.exe") {
		t.Fatalf("shared helper missing: %s", data)
	}
}

func TestTakeoverSettingPersists(t *testing.T) {
	dir := t.TempDir()
	launches, err := New(dir, testService(t, dir), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if err := launches.SetTakeoverEnabled(true); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(dir, testService(t, dir), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.TakeoverEnabled() {
		t.Fatal("takeover setting was not persisted")
	}
}
