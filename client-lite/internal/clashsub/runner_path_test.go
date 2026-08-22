package clashsub

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFindMihomoRequiresDedicatedDirectory(t *testing.T) {
	t.Setenv("EASY_NET_MIHOMO", "")
	directory := t.TempDir()
	name := "mihomo"
	if runtime.GOOS == "windows" {
		name = "mihomo.exe"
	}
	rootExecutable := filepath.Join(directory, name)
	if err := os.WriteFile(rootExecutable, []byte("root"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := findMihomo(directory); err == nil {
		t.Fatal("root-level mihomo must not be accepted")
	}

	dedicated := filepath.Join(directory, "mihomo", name)
	if err := os.MkdirAll(filepath.Dir(dedicated), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dedicated, []byte("nested"), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := findMihomo(directory)
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := filepath.Abs(dedicated)
	if found != expected {
		t.Fatalf("findMihomo() = %q, want %q", found, expected)
	}
}
