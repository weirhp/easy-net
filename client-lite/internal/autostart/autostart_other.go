//go:build !windows

package autostart

func platformSupported() bool { return false }

func readPlatformEntry(string) (string, bool, error) { return "", false, nil }

func writePlatformEntry(string, string) error { return nil }

func deletePlatformEntry(string) error { return nil }
