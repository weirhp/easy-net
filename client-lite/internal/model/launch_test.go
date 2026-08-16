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

func TestLaunchEntryAcceptsManualProxy(t *testing.T) {
	entry := LaunchEntry{ID: "manual", Name: "App", Mode: LaunchModeHook, Proxy: "127.0.0.1:10808", Path: `D:\app.exe`}
	if err := entry.ValidateForStart(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestLaunchEntryRejectsProfileAndManualProxyTogether(t *testing.T) {
	entry := LaunchEntry{ID: "both", Name: "App", Mode: LaunchModeChatGPT, ProfileID: "p1", Proxy: "127.0.0.1:10808"}
	if err := entry.Validate(); err == nil {
		t.Fatal("expected conflicting proxy sources to be rejected")
	}
}

func TestLaunchEntryRejectsHostnameProxy(t *testing.T) {
	entry := LaunchEntry{ID: "host", Name: "App", Mode: LaunchModeChatGPT, Proxy: "localhost:10808"}
	if err := entry.Validate(); err == nil {
		t.Fatal("expected non-literal proxy address to be rejected")
	}
}

func TestLaunchEntryNormalizesBrowserAttach(t *testing.T) {
	entry := LaunchEntry{
		ID: "edge", Name: "Edge", Mode: LaunchModeEdge, ProfileID: "p1",
		AttachExisting: true, Isolated: true, Arguments: "https://example.com", DNS: "1.1.1.1",
	}
	entry.Normalize()
	if !entry.AttachExisting || !entry.Isolated || entry.Arguments == "" || entry.DNS == "" || entry.UDPMode != "auto" {
		t.Fatalf("unexpected normalized entry: %#v", entry)
	}
	if err := entry.ValidateForStart(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestLaunchEntryAllowsTakeoverRuleForHook(t *testing.T) {
	entry := LaunchEntry{
		ID: "hook", Name: "Hook", Mode: LaunchModeHook, ProfileID: "p1",
		Path: `D:\app.exe`, AttachExisting: true,
	}
	// Normalize deliberately clears an unsupported persisted flag. Direct
	// validation still protects callers that skip normalization.
	if err := entry.Validate(); err != nil {
		t.Fatalf("takeover hook entry should be valid: %v", err)
	}
}
