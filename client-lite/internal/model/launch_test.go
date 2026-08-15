package model

import "testing"

func TestLaunchEntryRejectsUnsafeID(t *testing.T) {
	entry := LaunchEntry{ID: "bad/id", Name: "App", Mode: LaunchModeChatGPT}
	if err := entry.Validate(); err == nil {
		t.Fatal("expected unsafe launch ID to be rejected")
	}
}

func TestLaunchEntryAcceptsGeneratedStyleID(t *testing.T) {
	entry := LaunchEntry{ID: "abcDEF0123-_", Name: "App", Mode: LaunchModeChatGPT}
	if err := entry.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}
