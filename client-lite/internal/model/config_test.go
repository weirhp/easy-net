package model

import "testing"

func TestProfileCloneCopiesNestedConfigs(t *testing.T) {
	original := Profile{WebSocket: &WebSocketConfig{URL: "wss://one"}, SSH: &SSHConfig{Host: "one"}}
	clone := original.Clone()
	clone.WebSocket.URL = "wss://two"
	clone.SSH.Host = "two"
	if original.WebSocket.URL != "wss://one" || original.SSH.Host != "one" {
		t.Fatal("clone mutated original nested config")
	}
}

func TestExternalProxyUsesConfiguredEndpoint(t *testing.T) {
	profile := Profile{ID: "clash", Name: "Clash", Type: ProxyTypeExternal, ListenHost: "127.0.0.1", ListenPort: 7890, AutoStart: true, BypassPrivate: true}
	profile.Normalize()
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	if profile.ListenAddress() != "127.0.0.1:7890" || profile.AutoStart || profile.BypassPrivate {
		t.Fatalf("unexpected external proxy: %#v", profile)
	}
}

func TestDelayTarget(t *testing.T) {
	cases := []struct {
		name    string
		profile Profile
		host    string
		port    int
	}{
		{name: "ws default", profile: Profile{Type: ProxyTypeWebSocket, WebSocket: &WebSocketConfig{URL: "wss://proxy.example.com/tunnel"}}, host: "proxy.example.com", port: 443},
		{name: "ws custom", profile: Profile{Type: ProxyTypeWebSocket, WebSocket: &WebSocketConfig{URL: "ws://proxy.example.com:8080/path"}}, host: "proxy.example.com", port: 8080},
		{name: "ssh", profile: Profile{Type: ProxyTypeSSH, SSH: &SSHConfig{Host: "ssh.example.com", Port: 2222}}, host: "ssh.example.com", port: 2222},
		{name: "external", profile: Profile{Type: ProxyTypeExternal, ListenHost: "127.0.0.1", ListenPort: 10808}, host: "127.0.0.1", port: 10808},
	}
	for _, item := range cases {
		host, port, err := item.profile.DelayTarget()
		if err != nil {
			t.Fatalf("%s: %v", item.name, err)
		}
		if host != item.host || port != item.port {
			t.Fatalf("%s = %s:%d, want %s:%d", item.name, host, port, item.host, item.port)
		}
	}
	if _, _, err := (Profile{Type: ProxyTypeWebSocket}).DelayTarget(); err == nil {
		t.Fatal("expected invalid websocket target")
	}
}

func TestClashProfileRequiresSubscription(t *testing.T) {
	profile := Profile{ID: "clash-abc", Name: "机场", Type: ProxyTypeClash, ListenHost: "127.0.0.1", ListenPort: 17890, AutoStart: true, Clash: &ClashConfig{SubscriptionID: "abc", NodeName: "香港"}}
	profile.Normalize()
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	if profile.AutoStart || profile.Clash.SubscriptionID != "abc" {
		t.Fatalf("unexpected clash profile: %#v", profile)
	}
	profile.Clash = nil
	if err := profile.Validate(); err == nil {
		t.Fatal("expected clash profile without subscription id to fail")
	}
}
