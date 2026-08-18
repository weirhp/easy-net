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
