package clashsub

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"easy-net/client-lite/internal/model"

	"gopkg.in/yaml.v3"
)

func Parse(data []byte) ([]model.ClashNode, error) {
	payload := bytes.TrimPrefix(bytes.TrimSpace(data), []byte{0xef, 0xbb, 0xbf})
	if len(payload) == 0 {
		return nil, fmt.Errorf("订阅内容为空")
	}
	nodes, err := parseYAML(payload)
	if err == nil && len(nodes) > 0 {
		return nodes, nil
	}
	decoded, decodeErr := decodeMaybeBase64(payload)
	if decodeErr != nil {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("订阅中没有可用的 Clash 节点")
	}
	nodes, yamlErr := parseYAML(decoded)
	if yamlErr != nil {
		if err != nil {
			return nil, err
		}
		return nil, yamlErr
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("订阅中没有可用的 Clash 节点")
	}
	return nodes, nil
}

func parseYAML(data []byte) ([]model.ClashNode, error) {
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("解析 Clash YAML：%w", err)
	}
	rawNodes, _ := document["proxies"].([]any)
	nodes := make([]model.ClashNode, 0, len(rawNodes))
	seen := map[string]struct{}{}
	for _, item := range rawNodes {
		proxy, _ := item.(map[string]any)
		node, ok := clashNodeFromMap(proxy)
		if !ok {
			continue
		}
		if _, exists := seen[node.Name]; exists {
			continue
		}
		seen[node.Name] = struct{}{}
		nodes = append(nodes, node)
		if len(nodes) >= model.MaxClashNodes {
			break
		}
	}
	return nodes, nil
}

func clashNodeFromMap(proxy map[string]any) (model.ClashNode, bool) {
	if proxy == nil {
		return model.ClashNode{}, false
	}
	name := strings.TrimSpace(asString(proxy["name"]))
	kind := strings.TrimSpace(asString(proxy["type"]))
	if name == "" || kind == "" {
		return model.ClashNode{}, false
	}
	if kind == "direct" || kind == "reject" || kind == "selector" || kind == "url-test" || kind == "fallback" || kind == "load-balance" || kind == "relay" {
		return model.ClashNode{}, false
	}
	return model.ClashNode{
		Name:   name,
		Type:   kind,
		Server: strings.TrimSpace(asString(proxy["server"])),
		Port:   asInt(proxy["port"]),
		Raw:    normalizeMap(proxy),
	}, true
}

func decodeMaybeBase64(data []byte) ([]byte, error) {
	text := strings.TrimSpace(string(data))
	text = strings.ReplaceAll(text, "\n", "")
	text = strings.ReplaceAll(text, "\r", "")
	text = strings.ReplaceAll(text, " ", "")
	if text == "" {
		return nil, fmt.Errorf("empty")
	}
	decoded, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(text)
	}
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(text)
	}
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(decoded) {
		return nil, fmt.Errorf("not utf-8")
	}
	return decoded, nil
}

func asString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprintf("%v", value)
	}
}

func asInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case uint64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	default:
		return 0
	}
}

func normalizeMap(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = normalizeValue(item)
	}
	return out
}

func normalizeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return normalizeMap(typed)
	case map[any]any:
		converted := make(map[string]any, len(typed))
		for key, item := range typed {
			converted[fmt.Sprint(key)] = normalizeValue(item)
		}
		return converted
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = normalizeValue(item)
		}
		return out
	case float64:
		if typed == float64(int64(typed)) {
			return int(typed)
		}
		return typed
	default:
		return typed
	}
}
