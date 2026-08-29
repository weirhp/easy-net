package clashsub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"easy-net/client-lite/internal/model"
)

var errNotShareLinkSubscription = errors.New("not a v2rayN share-link subscription")

func parseShareLinks(data []byte) ([]model.ClashNode, error) {
	fields := strings.Fields(string(data))
	nodes := make([]model.ClashNode, 0, len(fields))
	seen := make(map[string]int)
	recognized := 0
	var firstErr error
	for _, field := range fields {
		field = strings.TrimSpace(field)
		var (
			node model.ClashNode
			err  error
		)
		switch {
		case strings.HasPrefix(strings.ToLower(field), "vmess://"):
			recognized++
			node, err = parseVMessLink(field)
		case strings.HasPrefix(strings.ToLower(field), "vless://"):
			recognized++
			node, err = parseVLESSLink(field)
		default:
			continue
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		node = uniqueShareNodeName(node, seen)
		nodes = append(nodes, node)
		if len(nodes) >= model.MaxClashNodes {
			break
		}
	}
	if recognized == 0 {
		return nil, errNotShareLinkSubscription
	}
	if len(nodes) == 0 {
		if firstErr != nil {
			return nil, fmt.Errorf("解析 v2rayN 订阅：%w", firstErr)
		}
		return nil, fmt.Errorf("v2rayN 订阅中没有可用节点")
	}
	return nodes, nil
}

func parseVMessLink(raw string) (model.ClashNode, error) {
	payload := strings.TrimSpace(raw[len("vmess://"):])
	decoded, err := decodeLinkBase64(payload)
	if err != nil {
		return model.ClashNode{}, fmt.Errorf("VMess 分享链接不是有效的 Base64")
	}
	var values map[string]any
	if err := json.Unmarshal(decoded, &values); err != nil {
		return model.ClashNode{}, fmt.Errorf("VMess 分享链接 JSON 无效")
	}
	server := strings.TrimSpace(asString(values["add"]))
	port := flexibleInt(values["port"])
	uuid := strings.TrimSpace(asString(values["id"]))
	if server == "" || port < 1 || port > 65535 || uuid == "" {
		return model.ClashNode{}, fmt.Errorf("VMess 分享链接缺少服务器、端口或 UUID")
	}
	name := cleanShareNodeName(asString(values["ps"]), "VMess", server, port)
	rawNode := map[string]any{
		"name": name, "type": "vmess", "server": server, "port": port,
		"uuid": uuid, "alterId": flexibleInt(values["aid"]), "cipher": "auto", "udp": true,
	}
	if cipher := strings.TrimSpace(asString(values["scy"])); cipher != "" {
		rawNode["cipher"] = cipher
	}
	if err := applyTLSOptions(rawNode, strings.TrimSpace(asString(values["tls"])), mapValues(values)); err != nil {
		return model.ClashNode{}, err
	}
	if err := applyTransportOptions(rawNode, transportSpec{
		Network: asString(values["net"]), Path: asString(values["path"]),
		Host: asString(values["host"]), ServiceName: firstNonEmpty(asString(values["serviceName"]), asString(values["path"])),
	}); err != nil {
		return model.ClashNode{}, fmt.Errorf("VMess %w", err)
	}
	return model.ClashNode{Name: name, Type: "vmess", Server: server, Port: port, Raw: rawNode}, nil
}

func parseVLESSLink(raw string) (model.ClashNode, error) {
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Scheme, "vless") {
		return model.ClashNode{}, fmt.Errorf("VLESS 分享链接无效")
	}
	server := strings.TrimSpace(parsed.Hostname())
	port, portErr := strconv.Atoi(parsed.Port())
	uuid := ""
	if parsed.User != nil {
		uuid = strings.TrimSpace(parsed.User.Username())
	}
	if server == "" || portErr != nil || port < 1 || port > 65535 || uuid == "" {
		return model.ClashNode{}, fmt.Errorf("VLESS 分享链接缺少服务器、端口或 UUID")
	}
	query := parsed.Query()
	name := cleanShareNodeName(parsed.Fragment, "VLESS", server, port)
	encryption := strings.TrimSpace(query.Get("encryption"))
	if strings.EqualFold(encryption, "none") {
		encryption = ""
	}
	rawNode := map[string]any{
		"name": name, "type": "vless", "server": server, "port": port,
		"uuid": uuid, "udp": true, "encryption": encryption,
	}
	if flow := strings.TrimSpace(query.Get("flow")); flow != "" && !strings.EqualFold(flow, "none") {
		rawNode["flow"] = flow
	}
	if err := applyTLSOptions(rawNode, query.Get("security"), query); err != nil {
		return model.ClashNode{}, err
	}
	if err := applyTransportOptions(rawNode, transportSpec{
		Network: query.Get("type"), Path: query.Get("path"), Host: query.Get("host"),
		ServiceName: query.Get("serviceName"), Mode: query.Get("mode"),
	}); err != nil {
		return model.ClashNode{}, fmt.Errorf("VLESS %w", err)
	}
	return model.ClashNode{Name: name, Type: "vless", Server: server, Port: port, Raw: rawNode}, nil
}

type transportSpec struct {
	Network     string
	Path        string
	Host        string
	ServiceName string
	Mode        string
}

func applyTransportOptions(node map[string]any, spec transportSpec) error {
	network := strings.ToLower(strings.TrimSpace(spec.Network))
	switch network {
	case "", "none", "tcp":
		return nil
	case "websocket":
		network = "ws"
	case "splithttp":
		network = "xhttp"
	}
	switch network {
	case "ws", "grpc", "h2", "http", "xhttp":
	default:
		return fmt.Errorf("分享链接使用了暂不支持的传输方式 %q", network)
	}
	node["network"] = network
	switch network {
	case "ws":
		opts := map[string]any{}
		if path := strings.TrimSpace(spec.Path); path != "" {
			opts["path"] = path
		}
		if host := strings.TrimSpace(spec.Host); host != "" {
			opts["headers"] = map[string]any{"Host": host}
		}
		if len(opts) > 0 {
			node["ws-opts"] = opts
		}
	case "grpc":
		if service := strings.TrimSpace(spec.ServiceName); service != "" {
			node["grpc-opts"] = map[string]any{"grpc-service-name": service}
		}
	case "h2":
		opts := map[string]any{}
		if host := strings.TrimSpace(spec.Host); host != "" {
			opts["host"] = splitCSV(host)
		}
		if path := strings.TrimSpace(spec.Path); path != "" {
			opts["path"] = path
		}
		if len(opts) > 0 {
			node["h2-opts"] = opts
		}
	case "http":
		opts := map[string]any{"method": "GET"}
		if path := strings.TrimSpace(spec.Path); path != "" {
			opts["path"] = []any{path}
		}
		if host := strings.TrimSpace(spec.Host); host != "" {
			opts["headers"] = map[string]any{"Host": splitCSV(host)}
		}
		node["http-opts"] = opts
	case "xhttp":
		opts := map[string]any{}
		if path := strings.TrimSpace(spec.Path); path != "" {
			opts["path"] = path
		}
		if host := strings.TrimSpace(spec.Host); host != "" {
			opts["host"] = host
		}
		if mode := strings.TrimSpace(spec.Mode); mode != "" && mode != "gun" {
			opts["mode"] = mode
		}
		if len(opts) > 0 {
			node["xhttp-opts"] = opts
		}
	}
	return nil
}

type stringValues interface {
	Get(string) string
}

type mapValueAdapter map[string]any

func (values mapValueAdapter) Get(key string) string { return asString(values[key]) }

func mapValues(values map[string]any) stringValues { return mapValueAdapter(values) }

func applyTLSOptions(node map[string]any, security string, values stringValues) error {
	security = strings.ToLower(strings.TrimSpace(security))
	if security != "tls" && security != "reality" {
		return nil
	}
	node["tls"] = true
	servername := firstNonEmpty(values.Get("sni"), values.Get("peer"))
	if servername == "" {
		servername = values.Get("authority")
	}
	if servername = strings.TrimSpace(servername); servername != "" {
		node["servername"] = servername
	}
	if fingerprint := strings.TrimSpace(values.Get("fp")); fingerprint != "" && !strings.EqualFold(fingerprint, "none") {
		node["client-fingerprint"] = fingerprint
	}
	if alpn := splitCSV(values.Get("alpn")); len(alpn) > 0 {
		node["alpn"] = alpn
	}
	if truthy(values.Get("allowInsecure")) || truthy(values.Get("insecure")) {
		node["skip-cert-verify"] = true
	}
	if security == "reality" {
		publicKey := strings.TrimSpace(values.Get("pbk"))
		if publicKey == "" {
			return fmt.Errorf("Reality 分享链接缺少公钥")
		}
		reality := map[string]any{}
		reality["public-key"] = publicKey
		if shortID := strings.TrimSpace(values.Get("sid")); shortID != "" {
			reality["short-id"] = shortID
		}
		if len(reality) > 0 {
			node["reality-opts"] = reality
		}
		if strings.TrimSpace(asString(node["client-fingerprint"])) == "" {
			node["client-fingerprint"] = "chrome"
		}
	}
	return nil
}

func decodeLinkBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	encodings := []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding}
	for _, encoding := range encodings {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("invalid base64")
}

func flexibleInt(value any) int {
	if result := asInt(value); result != 0 {
		return result
	}
	result, _ := strconv.Atoi(strings.TrimSpace(asString(value)))
	return result
}

func splitCSV(value string) []any {
	parts := strings.Split(value, ",")
	result := make([]any, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func cleanShareNodeName(value, protocol, server string, port int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	if value == "" {
		value = fmt.Sprintf("%s %s:%d", protocol, server, port)
	}
	runes := []rune(value)
	if len(runes) > 120 {
		value = string(runes[:120])
	}
	return value
}

func uniqueShareNodeName(node model.ClashNode, seen map[string]int) model.ClashNode {
	base := node.Name
	seen[base]++
	if seen[base] > 1 {
		node.Name = fmt.Sprintf("%s (%d)", base, seen[base])
		node.Raw["name"] = node.Name
	}
	return node
}
