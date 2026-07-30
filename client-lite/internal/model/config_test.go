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
