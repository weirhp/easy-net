package sharecode

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"easy-net/client-lite/internal/model"
)

const (
	Prefix          = "ENL1."
	CurrentVersion  = 1
	maxPayloadBytes = 128 * 1024
	maxCodeBytes    = 256 * 1024
)

var additionalData = []byte("Easy-Net-Lite/share-code/v1")

type Payload struct {
	Version       int              `json:"version"`
	Name          string           `json:"name"`
	Type          model.ProxyType  `json:"type"`
	PreferredPort int              `json:"preferredPort"`
	BypassPrivate bool             `json:"bypassPrivate,omitempty"`
	WebSocket     *WebSocketConfig `json:"websocket,omitempty"`
	SSH           *SSHConfig       `json:"ssh,omitempty"`
}

type WebSocketConfig struct {
	URL             string `json:"url"`
	Secret          string `json:"secret"`
	AllowInsecure   bool   `json:"allowInsecure,omitempty"`
	LegacyQueryAuth bool   `json:"legacyQueryAuth,omitempty"`
}

type SSHConfig struct {
	Host               string         `json:"host"`
	Port               int            `json:"port"`
	Username           string         `json:"username"`
	AuthType           model.AuthType `json:"authType"`
	Password           string         `json:"password,omitempty"`
	PrivateKey         string         `json:"privateKey,omitempty"`
	Passphrase         string         `json:"passphrase,omitempty"`
	HostKeyFingerprint string         `json:"hostKeyFingerprint,omitempty"`
}

func Encode(payload Payload) (string, error) {
	payload.Version = CurrentVersion
	if err := Validate(payload); err != nil {
		return "", err
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("序列化分享配置：%w", err)
	}
	if len(plaintext) > maxPayloadBytes {
		return "", fmt.Errorf("分享配置内容过大")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("生成分享密钥：%w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("初始化分享加密：%w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("初始化分享加密：%w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成分享随机数：%w", err)
	}
	sealed := aead.Seal(append([]byte(nil), nonce...), nonce, plaintext, additionalData)
	return Prefix + base64.RawURLEncoding.EncodeToString(key) + "." + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func Decode(code string) (Payload, error) {
	code = strings.TrimSpace(code)
	if len(code) == 0 || len(code) > maxCodeBytes || !strings.HasPrefix(code, Prefix) {
		return Payload{}, fmt.Errorf("不是有效的 Easy-Net Lite 分享码")
	}
	parts := strings.Split(strings.TrimPrefix(code, Prefix), ".")
	if len(parts) != 2 {
		return Payload{}, fmt.Errorf("分享码格式无效")
	}
	key, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(key) != 32 {
		return Payload{}, fmt.Errorf("分享码密钥无效")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Payload{}, fmt.Errorf("分享码内容无效")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Payload{}, fmt.Errorf("初始化分享解密：%w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Payload{}, fmt.Errorf("初始化分享解密：%w", err)
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() {
		return Payload{}, fmt.Errorf("分享码内容不完整")
	}
	nonce, ciphertext := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return Payload{}, fmt.Errorf("分享码已损坏或被修改")
	}
	if len(plaintext) > maxPayloadBytes {
		return Payload{}, fmt.Errorf("分享配置内容过大")
	}
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	var payload Payload
	if err := decoder.Decode(&payload); err != nil {
		return Payload{}, fmt.Errorf("解析分享配置：%w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Payload{}, fmt.Errorf("分享配置包含多余内容")
	}
	if err := Validate(payload); err != nil {
		return Payload{}, err
	}
	return payload, nil
}

func Validate(payload Payload) error {
	if payload.Version != CurrentVersion {
		return fmt.Errorf("不支持的分享码版本：%d", payload.Version)
	}
	if strings.TrimSpace(payload.Name) == "" || len([]rune(payload.Name)) > 80 {
		return fmt.Errorf("分享配置名称无效")
	}
	if payload.PreferredPort < 1 || payload.PreferredPort > 65535 {
		return fmt.Errorf("分享配置的本地端口无效")
	}
	switch payload.Type {
	case model.ProxyTypeWebSocket:
		if payload.WebSocket == nil || payload.SSH != nil || payload.WebSocket.Secret == "" {
			return fmt.Errorf("WebSocket 分享配置无效")
		}
		profile := model.Profile{
			ID: "share", Name: payload.Name, Type: model.ProxyTypeWebSocket, ListenHost: "127.0.0.1", ListenPort: payload.PreferredPort,
			WebSocket: &model.WebSocketConfig{URL: payload.WebSocket.URL, SecretRef: "share/websocket", AllowInsecure: payload.WebSocket.AllowInsecure, LegacyQueryAuth: payload.WebSocket.LegacyQueryAuth},
		}
		profile.Normalize()
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("WebSocket 分享配置无效：%w", err)
		}
	case model.ProxyTypeSSH:
		if payload.SSH == nil || payload.WebSocket != nil {
			return fmt.Errorf("SSH 分享配置无效")
		}
		if payload.SSH.AuthType == model.AuthTypePassword && payload.SSH.Password == "" {
			return fmt.Errorf("SSH 分享配置缺少密码")
		}
		if payload.SSH.AuthType == model.AuthTypePrivateKey {
			if payload.SSH.PrivateKey == "" {
				return fmt.Errorf("SSH 分享配置缺少私钥")
			}
			if len(payload.SSH.PrivateKey) > 64*1024 {
				return fmt.Errorf("SSH 私钥不能超过 64 KiB")
			}
		}
		if payload.SSH.HostKeyFingerprint != "" && (!strings.HasPrefix(payload.SSH.HostKeyFingerprint, "SHA256:") || len(payload.SSH.HostKeyFingerprint) > 128) {
			return fmt.Errorf("SSH 服务器指纹无效")
		}
		profile := model.Profile{
			ID: "share", Name: payload.Name, Type: model.ProxyTypeSSH, ListenHost: "127.0.0.1", ListenPort: payload.PreferredPort,
			SSH: &model.SSHConfig{Host: payload.SSH.Host, Port: payload.SSH.Port, Username: payload.SSH.Username, AuthType: payload.SSH.AuthType, PasswordRef: "share/password", PrivateKeyPath: "share.pem"},
		}
		if payload.SSH.AuthType == model.AuthTypePassword {
			profile.SSH.PrivateKeyPath = ""
		}
		profile.Normalize()
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("SSH 分享配置无效：%w", err)
		}
	default:
		return fmt.Errorf("分享配置类型无效")
	}
	return nil
}
