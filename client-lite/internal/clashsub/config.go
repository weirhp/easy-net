package clashsub

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func WriteMihomoConfig(path string, listenPort int, proxy map[string]any) error {
	if listenPort < 1 || listenPort > 65535 {
		return fmt.Errorf("本地端口无效")
	}
	if proxy == nil || strings.TrimSpace(asString(proxy["name"])) == "" {
		return fmt.Errorf("Clash 节点无效")
	}
	node := normalizeMap(proxy)
	name := asString(node["name"])
	document := map[string]any{
		"mixed-port":     listenPort,
		"bind-address":   "127.0.0.1",
		"allow-lan":      false,
		"mode":           "rule",
		"log-level":      "info",
		"ipv6":           false,
		"tcp-concurrent": false,
		"dns": map[string]any{
			"enable":                  true,
			"ipv6":                    false,
			"enhanced-mode":           "redir-host",
			"respect-rules":           true,
			"default-nameserver":      []any{"tcp://223.5.5.5:53", "tcp://119.29.29.29:53"},
			"proxy-server-nameserver": []any{"https://223.5.5.5/dns-query", "https://1.12.0.1/dns-query", "tls://223.5.5.5"},
			"nameserver":              []any{"https://8.8.8.8/dns-query", "tls://8.8.8.8"},
		},
		"proxies": []any{node},
		"proxy-groups": []any{
			map[string]any{"name": "PROXY", "type": "select", "proxies": []any{name}},
		},
		"rules": []any{"MATCH,PROXY"},
	}
	data, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("生成 Clash 运行配置：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("创建 Clash 运行目录：%w", err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		return fmt.Errorf("写入 Clash 运行配置：%w", err)
	}
	if err := replaceRuntimeFile(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("替换 Clash 运行配置：%w", err)
	}
	return nil
}
