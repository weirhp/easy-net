package clashsub

import (
	"errors"
	"testing"
)

func TestAccessTargetsCoverAISites(t *testing.T) {
	got := map[string]string{}
	for _, target := range accessTargets {
		got[target.ID] = target.Host
	}
	want := map[string]string{
		"gemini":  "gemini.google.com",
		"chatgpt": "chatgpt.com",
		"grok":    "grok.com",
		"claude":  "claude.ai",
	}
	for id, host := range want {
		if got[id] != host {
			t.Fatalf("%s host = %q, want %q", id, got[id], host)
		}
	}
}

func TestAccessErrorText(t *testing.T) {
	cases := map[string]string{
		"":                        "阻断",
		"i/o timeout":             "超时",
		"context deadline exceeded": "超时",
		"tls: handshake failure":  "握手失败",
		"SOCKS5 握手失败":            "握手失败",
		"connection refused":      "阻断",
	}
	for input, want := range cases {
		var err error
		if input != "" {
			err = errors.New(input)
		}
		if got := accessErrorText(err); got != want {
			t.Fatalf("accessErrorText(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTestAccessMissingSubscription(t *testing.T) {
	manager, err := New(t.TempDir(), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.TestAccess("missing", ""); err == nil {
		t.Fatal("expected missing subscription error")
	}
}

func TestFailedSiteResults(t *testing.T) {
	results := failedSiteResults("失败")
	if len(results) != len(accessTargets) {
		t.Fatalf("got %d results, want %d", len(results), len(accessTargets))
	}
	for i, result := range results {
		if result.OK || result.ID != accessTargets[i].ID || result.Error != "失败" {
			t.Fatalf("unexpected result %#v", result)
		}
	}
}
