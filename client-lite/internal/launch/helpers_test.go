package launch

import "testing"

func TestInferProcessHelpersUsesTreeAndPresets(t *testing.T) {
	selected := ProcessInfo{PID: 10, ParentPID: 1, Name: "ChatGPT.exe", Path: `C:\Apps\ChatGPT\ChatGPT.exe`}
	all := []ProcessInfo{
		selected,
		{PID: 11, ParentPID: 10, Name: "codex.exe", Path: `C:\Users\me\bin\codex.exe`},
		{PID: 12, ParentPID: 10, Name: "cmd.exe", Path: `C:\Windows\System32\cmd.exe`},
		{PID: 13, ParentPID: 1, Name: "notepad.exe", Path: `C:\Windows\System32\notepad.exe`},
	}
	got := InferProcessHelpers(selected, all)
	if !containsName(got, "codex.exe") || !containsName(got, "codex-code-mode-host.exe") {
		t.Fatalf("expected ChatGPT helpers, got %v", got)
	}
	if containsName(got, "cmd.exe") || containsName(got, "notepad.exe") || containsName(got, "ChatGPT.exe") {
		t.Fatalf("unexpected helper names: %v", got)
	}
}

func TestInferProcessHelpersSameDirectory(t *testing.T) {
	selected := ProcessInfo{PID: 20, Name: "chrome.exe", Path: `C:\Program Files\Google\Chrome\Application\chrome.exe`}
	all := []ProcessInfo{
		selected,
		{PID: 21, ParentPID: 20, Name: "chrome_proxy.exe", Path: `C:\Program Files\Google\Chrome\Application\chrome_proxy.exe`},
		{PID: 22, Name: "unrelated.exe", Path: `C:\Program Files\Google\Chrome\Application\unrelated.exe`},
	}
	got := InferProcessHelpers(selected, all)
	if !containsName(got, "chrome_proxy.exe") {
		t.Fatalf("expected same-dir related helper, got %v", got)
	}
	if containsName(got, "unrelated.exe") {
		t.Fatalf("did not expect unrelated same-dir exe: %v", got)
	}
}

func TestInferProcessHelpersIgnoresSystemDirectory(t *testing.T) {
	selected := ProcessInfo{PID: 30, Name: "notepad.exe", Path: `C:\Windows\System32\notepad.exe`}
	all := []ProcessInfo{
		selected,
		{PID: 31, Name: "calc.exe", Path: `C:\Windows\System32\calc.exe`},
	}
	if got := InferProcessHelpers(selected, all); len(got) != 0 {
		t.Fatalf("system directory should not collect neighbors: %v", got)
	}
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
