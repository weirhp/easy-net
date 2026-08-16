package main

import "testing"

func TestParseArgsBackground(t *testing.T) {
	launchID, launchSpec, openApps, background, showVersion := parseArgs([]string{"--background"})
	if launchID != "" || launchSpec != "" || openApps || !background || showVersion {
		t.Fatalf("unexpected flags: %q %q %v %v %v", launchID, launchSpec, openApps, background, showVersion)
	}
}

func TestParseArgsLaunchEntry(t *testing.T) {
	launchID, launchSpec, openApps, background, showVersion := parseArgs([]string{"--launch-entry", "abc123", "--launch-spec", "encoded"})
	if launchID != "abc123" || launchSpec != "encoded" || openApps || background || showVersion {
		t.Fatalf("unexpected flags: %q %q %v %v %v", launchID, launchSpec, openApps, background, showVersion)
	}
}
