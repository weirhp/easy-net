package launch

import (
	"path/filepath"
	"strings"
)

var knownProcessHelpers = map[string][]string{
	"chatgpt.exe":         {"codex-code-mode-host.exe", "codex.exe"},
	"antigravity ide.exe": {"language_server_windows_x64.exe"},
	"claude.exe":          {"claude-code.exe"},
	"claude-code.exe":     {"claude.exe"},
}

var genericHelperNames = map[string]struct{}{
	"cmd.exe": {}, "powershell.exe": {}, "pwsh.exe": {}, "conhost.exe": {},
	"explorer.exe": {}, "dllhost.exe": {}, "runtimebroker.exe": {},
	"svchost.exe": {}, "werfault.exe": {}, "openconsole.exe": {},
	"windowsterminal.exe": {}, "applicationframehost.exe": {},
	"searchhost.exe": {}, "sihost.exe": {}, "taskhostw.exe": {},
	"ctfmon.exe": {}, "fontdrvhost.exe": {}, "dwm.exe": {},
	"easy-net-lite.exe": {}, "easy-net-hook.exe": {}, "easy-net-windivert.exe": {},
	"mihomo.exe": {},
}

var helperNameKeywords = []string{
	"helper", "host", "sidecar", "crashpad", "gpu", "renderer", "service",
	"daemon", "worker", "languageserver", "language_server", "code-mode",
	"codex", "plugin", "extensionhost", "ptyhost",
}

// InferProcessHelpers finds companion executables for a selected process using
// known presets, the process tree, and nearby install-directory names.
func InferProcessHelpers(selected ProcessInfo, all []ProcessInfo) []string {
	selectedName := strings.TrimSpace(selected.Name)
	if selectedName == "" {
		return nil
	}
	names := []string{}
	seen := map[string]struct{}{strings.ToLower(selectedName): {}}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return
		}
		if _, generic := genericHelperNames[key]; generic {
			return
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	for _, name := range knownProcessHelpers[strings.ToLower(selectedName)] {
		add(name)
	}
	selectedDir := strings.ToLower(filepath.Clean(filepath.Dir(selected.Path)))
	sharedDir := selected.Path == "" || isSharedSystemDir(selectedDir)
	selectedPIDs := map[uint32]struct{}{}
	for _, item := range all {
		if selected.Path != "" && strings.EqualFold(item.Path, selected.Path) {
			selectedPIDs[item.PID] = struct{}{}
		}
	}
	if selected.PID != 0 {
		selectedPIDs[selected.PID] = struct{}{}
	}
	selectedStem := processStem(selectedName)
	for _, item := range all {
		if item.Name == "" || strings.EqualFold(item.Name, selectedName) {
			continue
		}
		if selected.Path != "" && strings.EqualFold(item.Path, selected.Path) {
			continue
		}
		itemDir := strings.ToLower(filepath.Clean(filepath.Dir(item.Path)))
		if isSharedSystemDir(itemDir) {
			continue
		}
		related := nameRelated(selectedStem, processStem(item.Name)) || looksLikeHelperName(item.Name)
		sameTree := !sharedDir && item.Path != "" && (itemDir == selectedDir || strings.HasPrefix(itemDir, selectedDir+string(filepath.Separator)) || strings.HasPrefix(selectedDir, itemDir+string(filepath.Separator)))
		_, child := selectedPIDs[item.ParentPID]
		sibling := selected.ParentPID != 0 && item.ParentPID == selected.ParentPID && related
		if child || sibling || (sameTree && related) {
			add(item.Name)
		}
	}
	return names
}

func isSharedSystemDir(dir string) bool {
	dir = strings.ToLower(strings.ReplaceAll(filepath.Clean(dir), "/", `\`))
	markers := []string{
		`\windows\system32`, `\windows\syswow64`, `\windows\winsxs`,
		`\windows\systemapps`, `\windows\immersivecontrolpanel`,
	}
	for _, marker := range markers {
		if strings.Contains(dir, marker) {
			return true
		}
	}
	base := filepath.Base(dir)
	return strings.EqualFold(base, "Windows") || strings.EqualFold(base, "System32") || strings.EqualFold(base, "SysWOW64")
}

func processStem(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimSuffix(name, ".exe")
	replacer := strings.NewReplacer(" ", "", "-", "", "_", "")
	return replacer.Replace(name)
}

func nameRelated(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	if len(left) >= 4 && len(right) >= 4 && (strings.Contains(left, right) || strings.Contains(right, left)) {
		return true
	}
	n := 0
	for n < len(left) && n < len(right) && left[n] == right[n] {
		n++
	}
	return n >= 4
}

func looksLikeHelperName(name string) bool {
	folded := strings.ToLower(name)
	for _, keyword := range helperNameKeywords {
		if strings.Contains(folded, keyword) {
			return true
		}
	}
	return false
}
