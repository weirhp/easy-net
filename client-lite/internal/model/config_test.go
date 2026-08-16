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
