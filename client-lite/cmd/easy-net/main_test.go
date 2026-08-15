package main

import "testing"

func TestParseArgsBackground(t *testing.T) {
	launchID, openApps, background, showVersion := parseArgs([]string{"--background"})
	if launchID != "" || openApps || !background || showVersion {
		t.Fatalf("unexpected flags: %q %v %v %v", launchID, openApps, background, showVersion)
	}
}

func TestParseArgsLaunchEntry(t *testing.T) {
	launchID, openApps, background, showVersion := parseArgs([]string{"--launch-entry", "abc123"})
	if launchID != "abc123" || openApps || background || showVersion {
		t.Fatalf("unexpected flags: %q %v %v %v", launchID, openApps, background, showVersion)
	}
}
