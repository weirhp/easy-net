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
	document := map[string]any{
		"mixed-port":                listenPort,
		"bind-address":              "127.0.0.1",
		"allow-lan":                 false,
		"mode":                      "global",
		"log-level":                 "info",
		"ipv6":                      false,
		"tcp-concurrent":            true,
		"global-client-fingerprint": "chrome",
		"dns": map[string]any{
			"enable":                  true,
			"ipv6":                    false,
			"enhanced-mode":           "redir-host",
			"default-nameserver":      []any{"223.5.5.5", "119.29.29.29"},
			"proxy-server-nameserver": []any{"https://dns.alidns.com/dns-query", "https://doh.pub/dns-query", "223.5.5.5"},
			"nameserver":              []any{"https://dns.alidns.com/dns-query", "https://doh.pub/dns-query"},
		},
		"proxies": []any{node},
	}
	data, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("生成 Clash 运行配置：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("创建 Clash 运行目录：%w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("写入 Clash 运行配置：%w", err)
	}
	return nil
}
