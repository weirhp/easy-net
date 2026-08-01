package sharecode

import (
	"encoding/base64"
	"strings"
	"testing"

	"easy-net/client-lite/internal/model"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	payload := Payload{
		Name: "测试 SSH", Type: model.ProxyTypeSSH, PreferredPort: 1080,
		SSH: &SSHConfig{Host: "ssh.example.com", Port: 22, Username: "user", AuthType: model.AuthTypePrivateKey, PrivateKey: "private-key", Passphrase: "phrase", HostKeyFingerprint: "SHA256:abcdefghijklmnopqrstuvwxyz0123456789ABCDE"},
	}
	code, err := Encode(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code, Prefix) || strings.Contains(code, "private-key") || strings.Contains(code, "phrase") {
		t.Fatalf("share code is not opaque: %s", code)
	}
	decoded, err := Decode(code)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SSH == nil || decoded.SSH.PrivateKey != payload.SSH.PrivateKey || decoded.SSH.Passphrase != payload.SSH.Passphrase {
		t.Fatalf("unexpected decoded payload: %#v", decoded)
	}
}

func TestDecodeRejectsTampering(t *testing.T) {
	code, err := Encode(Payload{Name: "WS", Type: model.ProxyTypeWebSocket, PreferredPort: 1080, WebSocket: &WebSocketConfig{URL: "wss://example.com", Secret: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimPrefix(code, Prefix), ".")
	if len(parts) != 2 {
		t.Fatalf("unexpected share code format: %q", code)
	}
	sealed, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(sealed) == 0 {
		t.Fatalf("decode sealed payload: %v", err)
	}
	sealed[len(sealed)-1] ^= 0x01
	code = Prefix + parts[0] + "." + base64.RawURLEncoding.EncodeToString(sealed)
	if _, err := Decode(code); err == nil {
		t.Fatal("expected tampered code rejection")
	}
}
