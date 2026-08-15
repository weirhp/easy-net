package launch

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"easy-net/client-lite/internal/model"
)

func TestSharedWinDivertProfileCombinesApplicationsAndProxies(t *testing.T) {
	dir := t.TempDir()
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
	var firstTCP, firstUDP, secondTCP bool
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
	}
	if !firstTCP || !firstUDP || !secondTCP {
		t.Fatalf("missing shared rules: firstTCP=%v firstUDP=%v secondTCP=%v", firstTCP, firstUDP, secondTCP)
	}
}

func TestSharedWinDivertProfileRejectsDuplicateProcessOwnership(t *testing.T) {
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
	if _, err := launches.writeSharedWinDivertProfile(); err == nil || !strings.Contains(err.Error(), "同时出现在多个") {
		t.Fatalf("expected duplicate process error, got %v", err)
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
