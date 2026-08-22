package launch

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"easy-net/client-lite/internal/model"
)

type bridgeProxyConfig struct {
	ID       int    `json:"Id"`
	Type     string `json:"Type"`
	Host     string `json:"Host"`
	Port     string `json:"Port"`
	Username string `json:"Username"`
	Password string `json:"Password"`
}

type bridgeProxyRule struct {
	ProcessName  string `json:"ProcessName"`
	TargetHosts  string `json:"TargetHosts"`
	TargetPorts  string `json:"TargetPorts"`
	TargetDomain string `json:"TargetDomains"`
	Protocol     string `json:"Protocol"`
	Action       string `json:"Action"`
	Enabled      bool   `json:"IsEnabled"`
	ProxyID      int    `json:"ProxyConfigId"`
}

type bridgeProfile struct {
	Version        string              `json:"Version"`
	LocalhostProxy bool                `json:"LocalhostViaProxy"`
	TrafficLogging bool                `json:"IsTrafficLoggingEnabled"`
	ProxyConfigs   []bridgeProxyConfig `json:"ProxyConfigs"`
	ProxyRules     []bridgeProxyRule   `json:"ProxyRules"`
}

var privateTargetRanges = []string{
	"10.0.0.0-10.255.255.255",
	"100.64.0.0-100.127.255.255",
	"169.254.0.0-169.254.255.255",
	"172.16.0.0-172.31.255.255",
	"192.168.0.0-192.168.255.255",
	"fc00::/7",
	"fe80::/10",
}

func (s *Service) writeSharedWinDivertProfile() (string, error) {
	entries := s.List()
	if !s.TakeoverEnabled() {
		entries = nil
	}
	profile := bridgeProfile{
		Version: "1.0", ProxyConfigs: []bridgeProxyConfig{}, ProxyRules: []bridgeProxyRule{},
	}
	proxyIDs := make(map[string]int)
	type route struct {
		host      string
		port      string
		proxyID   int
		udpAction string
	}
	type assignment struct {
		name     string
		routeKey string
		priority int
	}
	routes := make(map[string]route)
	assignments := make(map[string]assignment)
	routeOrder := make([]string, 0)
	for _, entry := range entries {
		if !usesSharedWinDivert(entry) {
			continue
		}
		proxyAddress, err := s.entryProxyAddress(entry)
		if err != nil {
			return "", fmt.Errorf("%s：%w", entry.Name, err)
		}
		host, port, err := splitProxyAddress(proxyAddress)
		if err != nil {
			return "", fmt.Errorf("%s：%w", entry.Name, err)
		}
		proxyID, ok := proxyIDs[proxyAddress]
		if !ok {
			proxyID = len(profile.ProxyConfigs) + 1
			proxyIDs[proxyAddress] = proxyID
			profile.ProxyConfigs = append(profile.ProxyConfigs, bridgeProxyConfig{
				ID: proxyID, Type: "socks5", Host: host, Port: port,
			})
		}
		udpAction := "PROXY"
		if entry.UDPMode == "block" {
			udpAction = "BLOCK"
		} else if entry.UDPMode == "direct" {
			udpAction = "DIRECT"
		}
		routeKey := strings.ToLower(proxyAddress) + "\x00" + udpAction
		if _, exists := routes[routeKey]; !exists {
			routes[routeKey] = route{host: host, port: port, proxyID: proxyID, udpAction: udpAction}
			routeOrder = append(routeOrder, routeKey)
		}
		processes, err := winDivertProcessNames(entry)
		if err != nil {
			return "", fmt.Errorf("%s：%w", entry.Name, err)
		}
		for index, process := range processes {
			key := strings.ToLower(process)
			priority := 1
			if index == 0 {
				priority = 2
			}
			current, exists := assignments[key]
			// Electron applications often share helper names. Identical routes are
			// deduplicated. If routes disagree, a primary executable wins over a
			// helper; otherwise the first stable entry wins because one process
			// cannot use two SOCKS routes simultaneously.
			if !exists || current.routeKey == routeKey || priority > current.priority {
				assignments[key] = assignment{name: process, routeKey: routeKey, priority: priority}
			}
		}
	}
	for _, routeKey := range routeOrder {
		currentRoute := routes[routeKey]
		processes := make([]string, 0)
		for _, item := range assignments {
			if item.routeKey == routeKey {
				processes = append(processes, item.name)
			}
		}
		if len(processes) == 0 {
			continue
		}
		slices.SortFunc(processes, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
		processList := strings.Join(processes, ";")
		// A shortcut-launched application may also use the native Chromium or
		// DLL Hook path. Its connection to the configured SOCKS5 server must
		// bypass WinDivert, otherwise a non-loopback proxy can be proxied again.
		profile.ProxyRules = append(profile.ProxyRules, newBridgeEndpointRule(processList, currentRoute.host, currentRoute.port, currentRoute.proxyID))
		for _, target := range privateTargetRanges {
			profile.ProxyRules = append(profile.ProxyRules, newBridgeRule(processList, target, "BOTH", "DIRECT", currentRoute.proxyID))
		}
		profile.ProxyRules = append(profile.ProxyRules, newBridgeRule(processList, "*", "TCP", "PROXY", currentRoute.proxyID))
		profile.ProxyRules = append(profile.ProxyRules, newBridgeRule(processList, "*", "UDP", currentRoute.udpAction, currentRoute.proxyID))
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return "", fmt.Errorf("生成共享 WinDivert 配置：%w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(filepath.Dir(s.store.Path()), "shared-windivert.pbprofile")
	temporary := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", fmt.Errorf("创建共享 WinDivert 配置目录：%w", err)
	}
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		return "", fmt.Errorf("写入共享 WinDivert 配置：%w", err)
	}
	if err := replaceFile(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("保存共享 WinDivert 配置：%w", err)
	}
	return path, nil
}

func (s *Service) entryProxyAddress(entry model.LaunchEntry) (string, error) {
	if entry.Proxy != "" {
		return entry.Proxy, nil
	}
	if s.proxies == nil {
		return "", fmt.Errorf("代理服务不可用")
	}
	profileID := entry.ProfileID
	var profile model.Profile
	var ok bool
	if profileID == "" {
		profile, ok = s.proxies.DefaultProfile()
	} else {
		profile, ok = s.proxies.Profile(profileID)
	}
	if !ok {
		if profileID == "" {
			return "", fmt.Errorf("尚未设置默认代理")
		}
		return "", fmt.Errorf("代理配置不存在")
	}
	return profile.ListenAddress(), nil
}

func splitProxyAddress(address string) (string, string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", "", fmt.Errorf("代理地址无效：%w", err)
	}
	return strings.Trim(host, "[]"), port, nil
}

func winDivertProcessNames(entry model.LaunchEntry) ([]string, error) {
	names := []string{windowsExecutableBase(entry.Path)}
	if names[0] == "" {
		switch entry.Mode {
		case model.LaunchModeChatGPT:
			names = []string{"ChatGPT.exe", "codex-code-mode-host.exe", "codex.exe"}
		case model.LaunchModeAntigravity:
			names = []string{"Antigravity IDE.exe", "language_server_windows_x64.exe"}
		case model.LaunchModeCursor:
			names = []string{"Cursor.exe"}
		case model.LaunchModeChrome:
			names = []string{"chrome.exe"}
		case model.LaunchModeEdge:
			names = []string{"msedge.exe"}
		case model.LaunchModeClaude:
			names = []string{"claude.exe", "claude-code.exe"}
		case model.LaunchModeWeChat, model.LaunchModeWeChatWinDivert:
			names = []string{"Weixin.exe", "WeChat.exe", "WeChatApp.exe", "WeChatAppEx.exe", "WeChatBrowser.exe"}
		}
	}
	for _, name := range strings.FieldsFunc(entry.ProcessNames, func(r rune) bool { return r == ';' || r == ',' }) {
		name = strings.TrimSpace(name)
		if name != "" {
			names = append(names, name)
		}
	}
	seen := make(map[string]struct{}, len(names))
	unique := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" || name == "." || strings.ContainsAny(name, `\/:*?"<>|`) {
			return nil, fmt.Errorf("WinDivert 进程名无效")
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, name)
	}
	return unique, nil
}

func usesSharedWinDivert(entry model.LaunchEntry) bool {
	return !entry.TakeoverDisabled && (entry.Mode == model.LaunchModeWinDivert || entry.AttachExisting)
}

func windowsExecutableBase(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), `\/`)
	if index := strings.LastIndexAny(path, `\/`); index >= 0 {
		return path[index+1:]
	}
	return path
}

func newBridgeRule(processes, targets, protocol, action string, proxyID int) bridgeProxyRule {
	return bridgeProxyRule{
		ProcessName: processes, TargetHosts: targets, TargetPorts: "*", TargetDomain: "*",
		Protocol: protocol, Action: action, Enabled: true, ProxyID: proxyID,
	}
}

func newBridgeEndpointRule(processes, host, port string, proxyID int) bridgeProxyRule {
	return bridgeProxyRule{
		ProcessName: processes, TargetHosts: host, TargetPorts: port, TargetDomain: "*",
		Protocol: "BOTH", Action: "DIRECT", Enabled: true, ProxyID: proxyID,
	}
}
