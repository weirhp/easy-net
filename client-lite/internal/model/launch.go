package model

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

type LaunchMode string

const (
	LaunchModeChatGPT         LaunchMode = "chatgpt"
	LaunchModeAntigravity     LaunchMode = "antigravity"
	LaunchModeCursor          LaunchMode = "cursor"
	LaunchModeWeChat          LaunchMode = "wechat"
	LaunchModeWeChatWinDivert LaunchMode = "wechat-windivert"
	LaunchModeHook            LaunchMode = "hook"
	LaunchModeWinDivert       LaunchMode = "windivert"
	CurrentLaunchFileVersion             = 1
	MaxLaunchEntries                     = 30
)

type LaunchFile struct {
	Version int           `json:"version"`
	Entries []LaunchEntry `json:"entries"`
}

type LaunchEntry struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Mode           LaunchMode `json:"mode"`
	ProfileID      string     `json:"profileId"`
	Proxy          string     `json:"proxy,omitempty"`
	Path           string     `json:"path,omitempty"`
	Arguments      string     `json:"arguments,omitempty"`
	Isolated       bool       `json:"isolated,omitempty"`
	WeChatExisting bool       `json:"wechatExisting,omitempty"`
	UDPMode        string     `json:"udpMode,omitempty"`
	DNS            string     `json:"dns,omitempty"`
	ProcessNames   string     `json:"processNames,omitempty"`
}

func (e LaunchEntry) Clone() LaunchEntry { return e }

func (e *LaunchEntry) Normalize() {
	e.ID = strings.TrimSpace(e.ID)
	e.Name = strings.TrimSpace(e.Name)
	e.Mode = LaunchMode(strings.TrimSpace(string(e.Mode)))
	e.ProfileID = strings.TrimSpace(e.ProfileID)
	e.Proxy = strings.TrimSpace(e.Proxy)
	e.Path = strings.TrimSpace(e.Path)
	e.Arguments = strings.TrimSpace(e.Arguments)
	e.UDPMode = strings.ToLower(strings.TrimSpace(e.UDPMode))
	e.DNS = strings.TrimSpace(e.DNS)
	e.ProcessNames = strings.TrimSpace(e.ProcessNames)
	if e.Mode != LaunchModeAntigravity && e.Mode != LaunchModeCursor {
		e.Isolated = false
	}
	if e.Mode != LaunchModeWeChat && e.Mode != LaunchModeWeChatWinDivert && e.Mode != LaunchModeWinDivert {
		e.WeChatExisting = false
		e.UDPMode = ""
	} else if e.UDPMode == "" {
		e.UDPMode = "auto"
	}
	if e.Mode != LaunchModeWinDivert {
		e.ProcessNames = ""
	}
	if e.WeChatExisting {
		e.Path = ""
		e.Arguments = ""
	}
	if e.Mode == LaunchModeChatGPT {
		e.DNS = ""
	}
}

func (e LaunchEntry) Validate() error {
	if !validLaunchID(e.ID) {
		return fmt.Errorf("启动入口 ID 无效")
	}
	if e.Name == "" || len([]rune(e.Name)) > 80 {
		return fmt.Errorf("启动入口名称无效")
	}
	switch e.Mode {
	case LaunchModeChatGPT, LaunchModeAntigravity, LaunchModeCursor, LaunchModeWeChat, LaunchModeWeChatWinDivert:
	case LaunchModeHook, LaunchModeWinDivert:
		if e.Path == "" {
			return fmt.Errorf("通用场景需要填写可执行文件路径")
		}
	default:
		return fmt.Errorf("不支持的启动场景：%s", e.Mode)
	}
	if e.UDPMode != "" && e.UDPMode != "auto" && e.UDPMode != "proxy" && e.UDPMode != "block" && e.UDPMode != "direct" {
		return fmt.Errorf("UDP 策略无效")
	}
	if e.ProfileID != "" && e.Proxy != "" {
		return fmt.Errorf("代理配置和手动 SOCKS5 地址只能选择一种")
	}
	if e.Proxy != "" {
		if err := validateLiteralProxy(e.Proxy); err != nil {
			return err
		}
	}
	if e.ProcessNames != "" {
		for _, name := range strings.Split(strings.ReplaceAll(e.ProcessNames, ",", ";"), ";") {
			name = strings.TrimSpace(name)
			if name == "" || strings.ContainsAny(name, `\/:*?"<>|`) {
				return fmt.Errorf("辅助进程名无效，只填写例如 helper.exe 的文件名")
			}
		}
	}
	if len([]rune(e.Path)) > 32767 || len([]rune(e.Arguments)) > 32767 || len([]rune(e.DNS)) > 255 || len([]rune(e.ProcessNames)) > 4096 {
		return fmt.Errorf("启动入口的路径、参数或 DNS 过长")
	}
	return nil
}

func validLaunchID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func (e LaunchEntry) ValidateForStart() error {
	if err := e.Validate(); err != nil {
		return err
	}
	if e.ProfileID == "" && e.Proxy == "" {
		return fmt.Errorf("请选择 Lite 代理配置或填写手动 SOCKS5 地址")
	}
	return nil
}

func validateLiteralProxy(value string) error {
	host, portText, err := net.SplitHostPort(value)
	if err != nil || net.ParseIP(strings.Trim(host, "[]")) == nil {
		return fmt.Errorf("手动代理必须是 IP:端口，例如 127.0.0.1:10808")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("手动代理端口无效")
	}
	return nil
}

func (e LaunchMode) Label() string {
	switch e {
	case LaunchModeChatGPT:
		return "ChatGPT"
	case LaunchModeAntigravity:
		return "Antigravity IDE"
	case LaunchModeCursor:
		return "Cursor"
	case LaunchModeWeChat:
		return "微信 TUN"
	case LaunchModeWeChatWinDivert:
		return "微信 WinDivert"
	case LaunchModeHook:
		return "通用 Hook"
	case LaunchModeWinDivert:
		return "通用 WinDivert"
	default:
		return string(e)
	}
}
