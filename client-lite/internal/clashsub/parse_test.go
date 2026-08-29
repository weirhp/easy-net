package clashsub

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleYAML = `
proxies:
  - name: 香港 1
    type: ss
    server: 1.2.3.4
    port: 8388
    cipher: aes-128-gcm
    password: secret
  - name: selector
    type: selector
    proxies: ["香港 1"]
  - name: 日本 2
    type: vmess
    server: jp.example.com
    port: 443
    uuid: 11111111-1111-1111-1111-111111111111
`

func TestParseYAMLSkipsProxyGroups(t *testing.T) {
	nodes, err := Parse([]byte(sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Name != "香港 1" || nodes[1].Name != "日本 2" {
		t.Fatalf("unexpected nodes: %#v", nodes)
	}
	if nodes[0].Raw["password"] != "secret" || nodes[0].Port != 8388 {
		t.Fatalf("raw node lost fields: %#v", nodes[0])
	}
}

func TestParseBase64YAML(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(sampleYAML))
	nodes, err := Parse([]byte(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestParseBase64V2RayNSubscription(t *testing.T) {
	vmessJSON, err := json.Marshal(map[string]any{
		"v": "2", "ps": "VMess TCP", "add": "vmess.example.com", "port": "443",
		"id": "11111111-1111-1111-1111-111111111111", "aid": "0", "scy": "auto", "net": "tcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	vmess := "vmess://" + base64.RawStdEncoding.EncodeToString(vmessJSON)
	vless := "vless://22222222-2222-2222-2222-222222222222@vless.example.com:8443?encryption=none&security=reality&sni=www.example.com&fp=chrome&pbk=public-key&sid=abcd&type=tcp#VLESS%20Reality"
	grpc := "vless://33333333-3333-3333-3333-333333333333@grpc.example.com:443?encryption=none&security=tls&sni=grpc.example.com&type=grpc&serviceName=my-service&mode=gun#VLESS%20gRPC"
	payload := strings.Join([]string{vmess, vless, grpc}, "\n")
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))

	nodes, err := Parse([]byte(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}
	if nodes[0].Type != "vmess" || nodes[0].Raw["uuid"] == "" || nodes[0].Raw["cipher"] != "auto" {
		t.Fatalf("unexpected VMess conversion: %#v", nodes[0].Raw)
	}
	if nodes[1].Type != "vless" || nodes[1].Raw["tls"] != true || nodes[1].Raw["encryption"] != "" {
		t.Fatalf("unexpected VLESS conversion: %#v", nodes[1].Raw)
	}
	reality, _ := nodes[1].Raw["reality-opts"].(map[string]any)
	if reality["public-key"] != "public-key" || reality["short-id"] != "abcd" {
		t.Fatalf("reality options missing: %#v", nodes[1].Raw)
	}
	if nodes[1].Raw["client-fingerprint"] != "chrome" {
		t.Fatalf("Reality node should receive the compatible fingerprint default: %#v", nodes[1].Raw)
	}
	grpcOpts, _ := nodes[2].Raw["grpc-opts"].(map[string]any)
	if nodes[2].Raw["network"] != "grpc" || grpcOpts["grpc-service-name"] != "my-service" {
		t.Fatalf("gRPC options missing: %#v", nodes[2].Raw)
	}
}

func TestParseYAMLAddsRealityFingerprintDefault(t *testing.T) {
	nodes, err := Parse([]byte(`proxies:
  - name: reality
    type: vless
    server: reality.example.com
    port: 443
    uuid: 11111111-1111-1111-1111-111111111111
    tls: true
    reality-opts:
      public-key: test-public-key
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Raw["client-fingerprint"] != "chrome" {
		t.Fatalf("Reality YAML did not receive the compatible default: %#v", nodes)
	}
}

func TestParseDirectV2RayNLinksKeepsDuplicateNames(t *testing.T) {
	links := strings.Join([]string{
		"vless://11111111-1111-1111-1111-111111111111@one.example.com:443?security=tls&type=ws&host=cdn.example.com&path=%2Fws#Same",
		"vless://22222222-2222-2222-2222-222222222222@two.example.com:443?security=tls&type=ws&host=cdn.example.com&path=%2Fws#Same",
	}, "\n")
	nodes, err := Parse([]byte(links))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Name != "Same" || nodes[1].Name != "Same (2)" {
		t.Fatalf("duplicate names were not preserved: %#v", nodes)
	}
	wsOpts, _ := nodes[0].Raw["ws-opts"].(map[string]any)
	if nodes[0].Raw["network"] != "ws" || wsOpts["path"] != "/ws" {
		t.Fatalf("WebSocket options missing: %#v", nodes[0].Raw)
	}
}

func TestWriteMihomoConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := WriteMihomoConfig(path, 17890, map[string]any{
		"name": "香港 1", "type": "ss", "server": "1.2.3.4", "port": 8388,
	}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"mixed-port: 17890", "mode: rule", "log-level: warning", "ipv6: true", "name: 香港 1", "type: ss", "MATCH,PROXY", "redir-host", "proxy-server-nameserver", "use-system-hosts: false", "https://1.1.1.1/dns-query#PROXY", "direct-nameserver"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "dns:\n") || !strings.Contains(text, "  ipv6: false") {
		t.Fatalf("DNS must suppress AAAA answers while the engine keeps literal IPv6 support:\n%s", text)
	}
}

func TestWriteMihomoConfigDirectRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := WriteMihomoConfig(path, 17890, map[string]any{
		"name": "香港 1", "type": "ss", "server": "1.2.3.4", "port": 8388,
	}, true, true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"IP-CIDR,10.0.0.0/8,DIRECT", "IP-CIDR,100.64.0.0/10,DIRECT", "IP-CIDR6,fc00::/7,DIRECT", "RULE-SET,easy-net-cn,DIRECT", "MATCH,PROXY"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "GEOIP,") {
		t.Fatalf("config must not require a downloaded GeoIP database:\n%s", text)
	}
	chinaRules, err := os.ReadFile(filepath.Join(filepath.Dir(path), "easy-net-cn.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(chinaRules), "1.0.1.0/24") {
		t.Fatalf("embedded China IP rules were not written: %s", chinaRules)
	}
}

func TestWriteMihomoConfigKeepsRawFingerprintOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := WriteMihomoConfig(path, 17890, map[string]any{
		"name": "日本 07", "type": "trojan", "server": "jp.example.com", "port": 443, "password": "x",
	}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"name: PROXY", "MATCH,PROXY", "日本 07"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "client-fingerprint") {
		t.Fatal("fingerprint should only be written when the subscription node has it")
	}
}

func TestWriteMihomoConfigPreservesNodeFingerprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := WriteMihomoConfig(path, 17890, map[string]any{
		"name": "日本 07", "type": "trojan", "server": "jp.example.com", "port": 443, "password": "x",
		"client-fingerprint": "chrome",
	}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "client-fingerprint: chrome") {
		t.Fatal(string(data))
	}
}

func TestLastMihomoConnectError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihomo.log")
	err := os.WriteFile(path, []byte("time=1 level=warning msg=\"dial PROXY error: example.com:443 connect error: EOF\"\n"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	if got := lastMihomoConnectError(path); got != "入口连接被断开" {
		t.Fatalf("got %q", got)
	}
}

func TestWithoutProxyEnv(t *testing.T) {
	out := withoutProxyEnv([]string{"PATH=C:\\Windows", "HTTPS_PROXY=socks5://127.0.0.1:10808", "http_proxy=http://127.0.0.1:9", "HOME=C:\\Users\\me"})
	text := strings.Join(out, ";")
	if strings.Contains(strings.ToLower(text), "proxy") {
		t.Fatalf("proxy env leaked: %v", out)
	}
	if !strings.Contains(text, "PATH=") || !strings.Contains(text, "HOME=") {
		t.Fatalf("kept env missing: %v", out)
	}
}
