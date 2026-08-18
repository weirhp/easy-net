package clashsub

import (
	"encoding/base64"
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

func TestWriteMihomoConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := WriteMihomoConfig(path, 17890, map[string]any{
		"name": "香港 1", "type": "ss", "server": "1.2.3.4", "port": 8388,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"mixed-port: 17890", "mode: global", "name: 香港 1", "type: ss", "redir-host", "proxy-server-nameserver", "global-client-fingerprint: chrome"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
}
