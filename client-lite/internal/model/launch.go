package model

import (
	"fmt"
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
	Path           string     `json:"path,omitempty"`
	Arguments      string     `json:"arguments,omitempty"`
	Isolated       bool       `json:"isolated,omitempty"`
	WeChatExisting bool       `json:"wechatExisting,omitempty"`
	UDPMode        string     `json:"udpMode,omitempty"`
	DNS            string     `json:"dns,omitempty"`
}

func (e LaunchEntry) Clone() LaunchEntry { return e }

func (e *LaunchEntry) Normalize() {
	e.ID = strings.TrimSpace(e.ID)
	e.Name = strings.TrimSpace(e.Name)
	e.Mode = LaunchMode(strings.TrimSpace(string(e.Mode)))
	e.ProfileID = strings.TrimSpace(e.ProfileID)
	e.Path = strings.TrimSpace(e.Path)
	e.Arguments = strings.TrimSpace(e.Arguments)
	e.UDPMode = strings.ToLower(strings.TrimSpace(e.UDPMode))
	e.DNS = strings.TrimSpace(e.DNS)
	if e.Mode != LaunchModeAntigravity && e.Mode != LaunchModeCursor {
		e.Isolated = false
	}
	if e.Mode != LaunchModeWeChat && e.Mode != LaunchModeWeChatWinDivert {
		e.WeChatExisting = false
		e.UDPMode = ""
	} else if e.UDPMode == "" {
		e.UDPMode = "auto"
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
	case LaunchModeHook:
		if e.Path == "" {
			return fmt.Errorf("通用程序需要填写可执行文件路径")
		}
	default:
		return fmt.Errorf("不支持的启动场景：%s", e.Mode)
	}
	if e.UDPMode != "" && e.UDPMode != "auto" && e.UDPMode != "proxy" && e.UDPMode != "block" && e.UDPMode != "direct" {
		return fmt.Errorf("UDP 策略无效")
	}
	if len([]rune(e.Path)) > 32767 || len([]rune(e.Arguments)) > 32767 || len([]rune(e.DNS)) > 255 {
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
	if e.ProfileID == "" {
		return fmt.Errorf("请选择要使用的代理配置")
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
		return "通用程序"
	default:
		return string(e)
	}
}
