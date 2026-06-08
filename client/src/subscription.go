package main

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

type SubscriptionNode struct {
	Name     string
	Type     string
	Server   string
	Port     int
	Username string
	Password string
	UDP      bool
}

func (m *Manager) RegisterSubscriptionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/sub/clash.yaml", m.handleClashSubscription)
	mux.HandleFunc("/sub/v2rayn.txt", m.handleV2rayNSubscription)
	mux.HandleFunc("/sub/socks.txt", m.handleSocksLinks)
}

func (m *Manager) subscriptionNodes() []SubscriptionNode {
	m.mu.Lock()
	defer m.mu.Unlock()

	nodes := make([]SubscriptionNode, 0)
	for _, cfg := range m.cfg.EasyNetServers {
		if srv := m.easyNet[cfg.ID]; srv != nil && srv.Running() {
			nodes = append(nodes, SubscriptionNode{
				Name:   cfg.Name,
				Type:   "socks5",
				Server: "127.0.0.1",
				Port:   cfg.LocalPort,
				UDP:    false,
			})
		}
	}
	for _, cfg := range m.cfg.ExternalSocks5 {
		if cfg.Enabled {
			nodes = append(nodes, SubscriptionNode{
				Name:     cfg.Name,
				Type:     "socks5",
				Server:   cfg.Host,
				Port:     cfg.Port,
				Username: cfg.Username,
				Password: cfg.Password,
				UDP:      cfg.UDP,
			})
		}
	}
	mihomoRunning := m.mihomo != nil && m.mihomo.Running()
	for _, cfg := range m.cfg.Chains {
		if mihomoRunning && cfg.Enabled && cfg.ListenPort > 0 {
			nodes = append(nodes, SubscriptionNode{
				Name:   cfg.Name,
				Type:   "socks5",
				Server: "127.0.0.1",
				Port:   cfg.ListenPort,
				UDP:    false,
			})
		}
	}
	return nodes
}

func (m *Manager) handleClashSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Profile-Update-Interval", "6")
	_, _ = w.Write([]byte(GenerateClashSubscription(m.subscriptionNodes())))
}

func (m *Manager) handleV2rayNSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	links := GenerateSocksShareLinks(m.subscriptionNodes())
	body := base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func (m *Manager) handleSocksLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(strings.Join(GenerateSocksShareLinks(m.subscriptionNodes()), "\n")))
}

func GenerateClashSubscription(nodes []SubscriptionNode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "proxies:\n")
	if len(nodes) == 0 {
		fmt.Fprintf(&b, "  []\n")
	} else {
		for _, node := range nodes {
			fmt.Fprintf(&b, "  - name: %s\n", yamlQuote(node.Name))
			fmt.Fprintf(&b, "    type: socks5\n")
			fmt.Fprintf(&b, "    server: %s\n", yamlQuote(node.Server))
			fmt.Fprintf(&b, "    port: %d\n", node.Port)
			if node.Username != "" {
				fmt.Fprintf(&b, "    username: %s\n", yamlQuote(node.Username))
			}
			if node.Password != "" {
				fmt.Fprintf(&b, "    password: %s\n", yamlQuote(node.Password))
			}
			fmt.Fprintf(&b, "    udp: %s\n", boolText(node.UDP))
		}
	}
	fmt.Fprintf(&b, "\nproxy-groups:\n")
	fmt.Fprintf(&b, "  - name: Easy-Net\n")
	fmt.Fprintf(&b, "    type: select\n")
	fmt.Fprintf(&b, "    proxies:\n")
	if len(nodes) == 0 {
		fmt.Fprintf(&b, "      - DIRECT\n")
	} else {
		for _, node := range nodes {
			fmt.Fprintf(&b, "      - %s\n", yamlQuote(node.Name))
		}
		fmt.Fprintf(&b, "      - DIRECT\n")
	}
	fmt.Fprintf(&b, "\nrules:\n")
	fmt.Fprintf(&b, "  - GEOSITE,private,DIRECT\n")
	fmt.Fprintf(&b, "  - GEOSITE,cn,DIRECT\n")
	if len(nodes) > 0 {
		fmt.Fprintf(&b, "  - GEOSITE,geolocation-!cn,Easy-Net\n")
	}
	fmt.Fprintf(&b, "  - GEOIP,CN,DIRECT\n")
	if len(nodes) == 0 {
		fmt.Fprintf(&b, "  - MATCH,DIRECT\n")
	} else {
		fmt.Fprintf(&b, "  - MATCH,Easy-Net\n")
	}
	return b.String()
}

func GenerateSocksShareLinks(nodes []SubscriptionNode) []string {
	links := make([]string, 0, len(nodes))
	for _, node := range nodes {
		host := net.JoinHostPort(node.Server, fmt.Sprintf("%d", node.Port))
		userInfo := ""
		if node.Username != "" || node.Password != "" {
			auth := base64.StdEncoding.EncodeToString([]byte(node.Username + ":" + node.Password))
			userInfo = url.PathEscape(auth) + "@"
		}
		links = append(links, "socks://"+userInfo+host+"#"+url.PathEscape(node.Name))
	}
	return links
}

func subscriptionBaseURL(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = net.JoinHostPort("127.0.0.1", "18080")
	}
	return "http://" + host
}
