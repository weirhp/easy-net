package autostart

import "testing"

func TestStartupCommandQuotesExecutableAndUsesBackgroundMode(t *testing.T) {
	manager := &Manager{executable: `C:\Program Files\Easy-Net\Easy-Net-Lite.exe`}
	want := `"C:\Program Files\Easy-Net\Easy-Net-Lite.exe" --background`
	if got := manager.command(); got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}
