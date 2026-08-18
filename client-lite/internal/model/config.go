package model

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

const CurrentConfigVersion = 3

type ProxyType string

const (
	ProxyTypeWebSocket ProxyType = "websocket"
	ProxyTypeSSH       ProxyType = "ssh"
	ProxyTypeExternal  ProxyType = "external"
	ProxyTypeClash     ProxyType = "clash"
)

type AuthType string

const (
	AuthTypePassword   AuthType = "password"
	AuthTypePrivateKey AuthType = "private_key"
)

type Config struct {
	Version  int       `json:"version"`
	Profiles []Profile `json:"profiles"`
}

type Profile struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Type       ProxyType `json:"type"`
	ListenHost string    `json:"listenHost"`
	ListenPort int       `json:"listenPort"`
	AutoStart  bool      `json:"autoStart"`
	Default    bool      `json:"default,omitempty"`
	// BypassPrivate sends private, loopback, and link-local destinations through
	// the machine's normal network stack instead of the configured transport.
	BypassPrivate bool `json:"bypassPrivate,omitempty"`
	// BypassChina sends APNIC CN destinations through the local network stack.
	BypassChina bool             `json:"bypassChina,omitempty"`
	WebSocket   *WebSocketConfig `json:"websocket,omitempty"`
	SSH         *SSHConfig       `json:"ssh,omitempty"`
	Clash       *ClashConfig     `json:"clash,omitempty"`
}

type ClashConfig struct {
	SubscriptionID string `json:"subscriptionId"`
	NodeName       string `json:"nodeName,omitempty"`
}

type WebSocketConfig struct {
	URL             string `json:"url"`
	SecretRef       string `json:"secretRef,omitempty"`
	AllowInsecure   bool   `json:"allowInsecure,omitempty"`
	LegacyQueryAuth bool   `json:"legacyQueryAuth,omitempty"`
}

type SSHConfig struct {
	Host               string   `json:"host"`
	Port               int      `json:"port"`
	Username           string   `json:"username"`
	AuthType           AuthType `json:"authType"`
	PasswordRef        string   `json:"passwordRef,omitempty"`
	PrivateKeyPath     string   `json:"privateKeyPath,omitempty"`
	PassphraseRef      string   `json:"passphraseRef,omitempty"`
	HostKeyFingerprint string   `json:"hostKeyFingerprint,omitempty"`
}

func (p Profile) Clone() Profile {
	clone := p
	if p.WebSocket != nil {
		ws := *p.WebSocket
		clone.WebSocket = &ws
	}
	if p.SSH != nil {
		ssh := *p.SSH
		clone.SSH = &ssh
	}
	if p.Clash != nil {
		clash := *p.Clash
		clone.Clash = &clash
	}
	return clone
}

func (p *Profile) Normalize() {
	p.Name = strings.TrimSpace(p.Name)
	p.ListenHost = strings.TrimSpace(p.ListenHost)
	if p.ListenHost == "" {
		p.ListenHost = "127.0.0.1"
	}
	if p.WebSocket != nil {
		p.WebSocket.URL = strings.TrimSpace(p.WebSocket.URL)
	}
	if p.SSH != nil {
		p.SSH.Host = strings.TrimSpace(p.SSH.Host)
		p.SSH.Username = strings.TrimSpace(p.SSH.Username)
		p.SSH.PrivateKeyPath = strings.TrimSpace(p.SSH.PrivateKeyPath)
		if p.SSH.Port == 0 {
			p.SSH.Port = 22
		}
		if p.SSH.AuthType == "" {
			p.SSH.AuthType = AuthTypePassword
		}
	}
	if p.Type == ProxyTypeExternal || p.Type == ProxyTypeClash {
		p.AutoStart = false
		p.BypassPrivate = false
		p.BypassChina = false
		p.WebSocket = nil
		p.SSH = nil
	}
	if p.Clash != nil {
		p.Clash.SubscriptionID = strings.TrimSpace(p.Clash.SubscriptionID)
		p.Clash.NodeName = strings.TrimSpace(p.Clash.NodeName)
	}
}

func (p Profile) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("配置 ID 不能为空")
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("名称不能为空")
	}
	ip := net.ParseIP(p.ListenHost)
	if ip == nil || p.Type != ProxyTypeExternal && !ip.IsLoopback() {
		return fmt.Errorf("代理地址必须是有效 IP；Lite 自建代理仅允许监听本机回环地址")
	}
	if p.ListenPort < 1 || p.ListenPort > 65535 {
		return fmt.Errorf("本地端口必须在 1–65535 之间")
	}
	switch p.Type {
	case ProxyTypeExternal:
		if p.WebSocket != nil || p.SSH != nil || p.Clash != nil {
			return fmt.Errorf("外部 SOCKS5 配置不能包含 Lite 隧道参数")
		}
	case ProxyTypeClash:
		if p.WebSocket != nil || p.SSH != nil {
			return fmt.Errorf("Clash 订阅代理不能包含 Lite 隧道参数")
		}
		if p.Clash == nil || strings.TrimSpace(p.Clash.SubscriptionID) == "" {
			return fmt.Errorf("Clash 订阅 ID 不能为空")
		}
	case ProxyTypeWebSocket:
		if p.WebSocket == nil || strings.TrimSpace(p.WebSocket.URL) == "" {
			return fmt.Errorf("WebSocket 地址不能为空")
		}
		rawURL := strings.TrimSpace(p.WebSocket.URL)
		if !strings.Contains(rawURL, "://") {
			rawURL = "wss://" + rawURL
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Host == "" {
			return fmt.Errorf("WebSocket 地址无效")
		}
		if parsed.User != nil {
			return fmt.Errorf("WebSocket 地址不能包含用户名或密码")
		}
		if parsed.Query().Has("secret") {
			return fmt.Errorf("WebSocket 地址不能包含 secret 参数，请在密钥字段中单独填写")
		}
		if parsed.Scheme != "wss" && parsed.Scheme != "https" && parsed.Scheme != "ws" && parsed.Scheme != "http" {
			return fmt.Errorf("WebSocket 地址必须使用 wss:// 或 ws://")
		}
		if (parsed.Scheme == "ws" || parsed.Scheme == "http") && !p.WebSocket.AllowInsecure {
			return fmt.Errorf("未加密的 ws:// 连接必须明确允许")
		}
		if p.WebSocket.SecretRef == "" {
			return fmt.Errorf("WebSocket 密钥不能为空")
		}
	case ProxyTypeSSH:
		if p.SSH == nil {
			return fmt.Errorf("SSH 配置不能为空")
		}
		if p.SSH.Host == "" || p.SSH.Username == "" {
			return fmt.Errorf("SSH 地址和用户名不能为空")
		}
		if p.SSH.Port < 1 || p.SSH.Port > 65535 {
			return fmt.Errorf("SSH 端口必须在 1–65535 之间")
		}
		if p.SSH.AuthType != AuthTypePassword && p.SSH.AuthType != AuthTypePrivateKey {
			return fmt.Errorf("不支持的 SSH 认证方式")
		}
		if p.SSH.AuthType == AuthTypePrivateKey && p.SSH.PrivateKeyPath == "" {
			return fmt.Errorf("请选择 SSH 私钥文件")
		}
		if p.SSH.AuthType == AuthTypePassword && p.SSH.PasswordRef == "" {
			return fmt.Errorf("SSH 密码不能为空")
		}
	default:
		return fmt.Errorf("不支持的代理类型：%s", p.Type)
	}
	return nil
}

func (p Profile) Endpoint() string {
	switch p.Type {
	case ProxyTypeExternal, ProxyTypeClash:
		return p.ListenAddress()
	case ProxyTypeWebSocket:
		if p.WebSocket != nil {
			return p.WebSocket.URL
		}
	case ProxyTypeSSH:
		if p.SSH != nil {
			return net.JoinHostPort(p.SSH.Host, fmt.Sprintf("%d", p.SSH.Port))
		}
	}
	return ""
}

func (p Profile) ListenAddress() string {
	return net.JoinHostPort(p.ListenHost, fmt.Sprintf("%d", p.ListenPort))
}
