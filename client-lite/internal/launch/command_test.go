package launch

import (
	"reflect"
	"testing"

	"easy-net/client-lite/internal/model"
)

func TestHookArgsChatGPT(t *testing.T) {
	args, err := HookArgs(model.LaunchEntry{
		ID: "1", Name: "ChatGPT", Mode: model.LaunchModeChatGPT, ProfileID: "p",
	}, "127.0.0.1:1082")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--proxy", "127.0.0.1:1082", "--detach", "--gui-worker", "--chatgpt-app"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %#v want %#v", args, want)
	}
}

func TestHookArgsCursorIsolated(t *testing.T) {
	args, err := HookArgs(model.LaunchEntry{
		ID: "2", Name: "Cursor", Mode: model.LaunchModeCursor, ProfileID: "p",
		Path: `C:\Cursor\Cursor.exe`, Isolated: true, Arguments: "--reuse-window",
	}, "127.0.0.1:1083")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--proxy", "127.0.0.1:1083", "--detach", "--gui-worker", "--cursor",
		"--cursor-isolated", "--cursor-path", `C:\Cursor\Cursor.exe`, "--", "--reuse-window",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %#v want %#v", args, want)
	}
}

func TestHookArgsWeChatWinDivertExisting(t *testing.T) {
	args, err := HookArgs(model.LaunchEntry{
		ID: "3", Name: "微信", Mode: model.LaunchModeWeChatWinDivert, ProfileID: "p",
		WeChatExisting: true, UDPMode: "proxy", Path: `C:\WeChat\WeChat.exe`,
	}, "127.0.0.1:1084")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--proxy", "127.0.0.1:1084", "--detach", "--gui-worker",
		"--wechat-existing", "--udp-mode", "proxy",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %#v want %#v", args, want)
	}
}

func TestHookArgsGenericQuotedArguments(t *testing.T) {
	args, err := HookArgs(model.LaunchEntry{
		ID: "4", Name: "App", Mode: model.LaunchModeHook, ProfileID: "p",
		Path: `D:\app.exe`, Arguments: `--flag "quoted value"`, DNS: "1.1.1.1",
	}, "127.0.0.1:1085")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--proxy", "127.0.0.1:1085", "--detach", "--gui-worker", "--dns", "1.1.1.1",
		"--", `D:\app.exe`, "--flag", "quoted value",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %#v want %#v", args, want)
	}
}

func TestHookArgsRequiresProxyAndProfile(t *testing.T) {
	_, err := HookArgs(model.LaunchEntry{ID: "1", Name: "ChatGPT", Mode: model.LaunchModeChatGPT}, "127.0.0.1:1082")
	if err == nil {
		t.Fatal("expected missing profile error")
	}
	_, err = HookArgs(model.LaunchEntry{ID: "1", Name: "ChatGPT", Mode: model.LaunchModeChatGPT, ProfileID: "p"}, " ")
	if err == nil {
		t.Fatal("expected missing proxy error")
	}
}

func TestSplitArgumentsPreservesWindowsPaths(t *testing.T) {
	got := splitArguments(`--file C:\Users\name\test.txt --title "quoted value" ""`)
	want := []string{"--file", `C:\Users\name\test.txt`, "--title", "quoted value", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestSplitArgumentsHandlesBackslashesBeforeQuote(t *testing.T) {
	got := splitArguments(`--value "a\\\"b"`)
	want := []string{"--value", `a\"b`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestHookArgsGenericWinDivert(t *testing.T) {
	args, err := HookArgs(model.LaunchEntry{
		ID: "5", Name: "App", Mode: model.LaunchModeWinDivert, Proxy: "127.0.0.1:10808",
		Path: `D:\App\app.exe`, Arguments: `--flag "quoted value"`, UDPMode: "proxy",
		ProcessNames: "app.exe;helper.exe",
	}, "127.0.0.1:10808")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--proxy", "127.0.0.1:10808", "--detach", "--gui-worker", "--windivert",
		"--udp-mode", "proxy", "--windivert-processes", "app.exe;helper.exe", "--",
		`D:\App\app.exe`, "--flag", "quoted value",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %#v want %#v", args, want)
	}
}

func TestHookArgsChromeIsolated(t *testing.T) {
	args, err := HookArgs(model.LaunchEntry{
		ID: "6", Name: "Chrome", Mode: model.LaunchModeChrome, ProfileID: "p",
		Path: `C:\Program Files\Google\Chrome\Application\chrome.exe`, Isolated: true,
		Arguments: `https://example.com`,
	}, "127.0.0.1:1082")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--proxy", "127.0.0.1:1082", "--detach", "--gui-worker", "--chrome",
		"--browser-path", `C:\Program Files\Google\Chrome\Application\chrome.exe`,
		"--browser-isolated", "--", "https://example.com",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %#v want %#v", args, want)
	}
}

func TestHookArgsAttachRunningEdge(t *testing.T) {
	args, err := HookArgs(model.LaunchEntry{
		ID: "7", Name: "Edge", Mode: model.LaunchModeEdge, ProfileID: "p",
		AttachExisting: true, UDPMode: "block", ProcessNames: "edge-helper.exe",
	}, "127.0.0.1:1082")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--proxy", "127.0.0.1:1082", "--detach", "--gui-worker", "--windivert",
		"--windivert-existing", "--udp-mode", "block",
		"--windivert-processes", "msedge.exe;edge-helper.exe",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %#v want %#v", args, want)
	}
}

func TestHookArgsAttachRunningGenericWinDivert(t *testing.T) {
	args, err := HookArgs(model.LaunchEntry{
		ID: "8", Name: "Running App", Mode: model.LaunchModeWinDivert, Proxy: "127.0.0.1:10808",
		Path: `D:\App\app.exe`, AttachExisting: true, UDPMode: "proxy", ProcessNames: "app.exe",
	}, "127.0.0.1:10808")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--proxy", "127.0.0.1:10808", "--detach", "--gui-worker", "--windivert",
		"--udp-mode", "proxy", "--windivert-existing", "--windivert-processes", "app.exe",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %#v want %#v", args, want)
	}
}
