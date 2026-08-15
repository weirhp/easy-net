package proxy

import (
	"context"
	"testing"
)

func TestResolvePrivateTarget(t *testing.T) {
	tests := []struct {
		address string
		private bool
	}{
		{address: "192.168.0.252:8311", private: true},
		{address: "10.0.0.1:443", private: true},
		{address: "172.31.255.254:53", private: true},
		{address: "127.0.0.1:80", private: true},
		{address: "169.254.10.20:80", private: true},
		{address: "100.64.0.1:443", private: true},
		{address: "[::1]:80", private: true},
		{address: "[fd00::1]:443", private: true},
		{address: "[fe80::1%12]:53", private: true},
		{address: "8.8.8.8:53", private: false},
		{address: "[2606:4700:4700::1111]:53", private: false},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			_, private := resolvePrivateTarget(context.Background(), test.address)
			if private != test.private {
				t.Fatalf("resolvePrivateTarget(%q) private=%v, want %v", test.address, private, test.private)
			}
		})
	}
}

func TestResolvePrivateTargetUsesLocalDNSForPrivateHost(t *testing.T) {
	resolved, private := resolvePrivateTarget(context.Background(), "localhost:8311")
	if !private || resolved == "" {
		t.Fatalf("localhost should resolve as a private target, got %q private=%v", resolved, private)
	}
}

func TestResolveChinaTarget(t *testing.T) {
	tests := []struct {
		address string
		direct  bool
	}{
		{address: "1.0.1.1:443", direct: true},
		{address: "[2001:250::1]:443", direct: true},
		{address: "8.8.8.8:53", direct: false},
		{address: "[2606:4700:4700::1111]:53", direct: false},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			_, direct := resolveBypassTarget(context.Background(), test.address, false, true)
			if direct != test.direct {
				t.Fatalf("resolveBypassTarget(%q) direct=%v, want %v", test.address, direct, test.direct)
			}
		})
	}
}

func TestChinaRuleCanCombineWithPrivateRule(t *testing.T) {
	for _, address := range []string{"192.168.1.1:80", "1.0.1.1:80"} {
		if _, direct := resolveBypassTarget(context.Background(), address, true, true); !direct {
			t.Fatalf("%s should match the combined direct rules", address)
		}
	}
}
