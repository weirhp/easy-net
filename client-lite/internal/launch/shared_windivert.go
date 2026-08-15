package launch

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	profile := bridgeProfile{Version: "1.0"}
	proxyIDs := make(map[string]int)
	processOwners := make(map[string]string)
	for _, entry := range entries {
		if entry.Mode != model.LaunchModeWinDivert {
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
		processes, err := winDivertProcessNames(entry)
		if err != nil {
			return "", fmt.Errorf("%s：%w", entry.Name, err)
		}
		processList := strings.Join(processes, ";")
		for _, process := range processes {
			key := strings.ToLower(process)
			if owner, exists := processOwners[key]; exists && owner != entry.ID {
				return "", fmt.Errorf("进程 %s 同时出现在多个 WinDivert 应用中，请合并或删除重复规则", process)
			}
			processOwners[key] = entry.ID
		}
		for _, target := range privateTargetRanges {
			profile.ProxyRules = append(profile.ProxyRules, newBridgeRule(processList, target, "BOTH", "DIRECT", proxyID))
		}
		profile.ProxyRules = append(profile.ProxyRules, newBridgeRule(processList, "*", "TCP", "PROXY", proxyID))
		udpAction := "PROXY"
		if entry.UDPMode == "block" {
			udpAction = "BLOCK"
		} else if entry.UDPMode == "direct" {
			udpAction = "DIRECT"
		}
		profile.ProxyRules = append(profile.ProxyRules, newBridgeRule(processList, "*", "UDP", udpAction, proxyID))
	}
	if len(profile.ProxyRules) == 0 {
		return "", fmt.Errorf("共享 WinDivert 配置中没有应用规则")
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
	profile, ok := s.proxies.Profile(entry.ProfileID)
	if !ok {
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
