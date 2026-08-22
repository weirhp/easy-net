package clashsub

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	easynetproxy "easy-net/client-lite/internal/proxy"

	"gopkg.in/yaml.v3"
)

func WriteMihomoConfig(path string, listenPort int, proxy map[string]any, bypassPrivate, bypassChina bool) error {
	if listenPort < 1 || listenPort > 65535 {
		return fmt.Errorf("本地端口无效")
	}
	if proxy == nil || strings.TrimSpace(asString(proxy["name"])) == "" {
		return fmt.Errorf("Clash 节点无效")
	}
	node := normalizeMap(proxy)
	name := asString(node["name"])
	document := map[string]any{
		"mixed-port":   listenPort,
		"bind-address": "127.0.0.1",
		"allow-lan":    false,
		"mode":         "rule",
		"log-level":    "warning",
		// Keep the engine capable of forwarding literal IPv6 destinations, but
		// request only IPv4 DNS answers below. This avoids the previous conflict
		// where CN IPv6 rules matched addresses that Mihomo itself refused.
		"ipv6":           true,
		"tcp-concurrent": false,
		"dns": map[string]any{
			"enable":             true,
			"ipv6":               false,
			"enhanced-mode":      "redir-host",
			"respect-rules":      true,
			"prefer-h3":          false,
			"use-system-hosts":   false,
			"default-nameserver": []any{"https://223.5.5.5/dns-query", "https://1.12.12.12/dns-query"},
			// Node hostnames need a non-circular resolver before the selected
			// proxy is connected. These are encrypted domestic DoH endpoints.
			"proxy-server-nameserver": []any{"https://223.5.5.5/dns-query", "https://1.12.12.12/dns-query"},
			// Public application domains are resolved through the selected node.
			// A polluted Windows/router DNS response can therefore never decide a
			// CN direct rule. Mihomo's #PROXY selector is intentional here.
			"nameserver": []any{
				"https://1.1.1.1/dns-query#PROXY",
				"https://1.0.0.1/dns-query#PROXY",
			},
			"direct-nameserver": []any{
				"https://223.5.5.5/dns-query",
				"https://1.12.12.12/dns-query",
			},
			"direct-nameserver-follow-policy": false,
		},
		"proxies": []any{node},
		"proxy-groups": []any{
			map[string]any{"name": "PROXY", "type": "select", "proxies": []any{name}},
		},
		"rules": clashDirectRules(bypassPrivate, bypassChina),
	}
	if bypassChina {
		document["rule-providers"] = map[string]any{
			"easy-net-cn": map[string]any{
				"type": "file", "behavior": "ipcidr", "format": "text", "path": "./easy-net-cn.txt",
			},
		}
	}
	data, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("生成 Clash 运行配置：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("创建 Clash 运行目录：%w", err)
	}
	chinaPath := filepath.Join(filepath.Dir(path), "easy-net-cn.txt")
	if bypassChina {
		if err := replaceConfigFile(chinaPath, []byte(easynetproxy.ChinaPrefixData())); err != nil {
			return fmt.Errorf("写入中国大陆 IP 规则：%w", err)
		}
	} else {
		_ = os.Remove(chinaPath)
	}
	if err := replaceConfigFile(path, data); err != nil {
		return fmt.Errorf("替换 Clash 运行配置：%w", err)
	}
	return nil
}

func replaceConfigFile(path string, data []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	if err := replaceRuntimeFile(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func clashDirectRules(bypassPrivate, bypassChina bool) []any {
	rules := make([]any, 0, 16)
	if bypassPrivate {
		rules = append(rules,
			"DOMAIN-SUFFIX,local,DIRECT",
			"IP-CIDR,10.0.0.0/8,DIRECT",
			"IP-CIDR,100.64.0.0/10,DIRECT",
			"IP-CIDR,127.0.0.0/8,DIRECT",
			"IP-CIDR,169.254.0.0/16,DIRECT",
			"IP-CIDR,172.16.0.0/12,DIRECT",
			"IP-CIDR,192.168.0.0/16,DIRECT",
			"IP-CIDR,198.18.0.0/15,DIRECT",
			"IP-CIDR6,::1/128,DIRECT",
			"IP-CIDR6,fc00::/7,DIRECT",
			"IP-CIDR6,fe80::/10,DIRECT",
		)
	}
	if bypassChina {
		rules = append(rules, "RULE-SET,easy-net-cn,DIRECT")
	}
	return append(rules, "MATCH,PROXY")
}
