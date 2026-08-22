package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingFileCapsAndRetainsBackups(t *testing.T) {
	dir := t.TempDir()
	writer, err := NewRotatingFile(dir, "app.log", 16, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"12345678\n", "abcdefgh\n", "ABCDEFGH\n", "87654321\n"} {
		if _, err := writer.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app.log", "app.log.1"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) == 0 || strings.Contains(string(data), "12345678") {
			t.Fatalf("unexpected rotated content in %s: %q", name, data)
		}
	}
}

func TestRotatingFileHardCapsLargeWrites(t *testing.T) {
	dir := t.TempDir()
	writer, err := NewRotatingFile(dir, "large.log", 16, 2)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 80)
	n, err := writer.Write([]byte(payload))
	if err != nil || n != len(payload) {
		t.Fatalf("large write: n=%d err=%v", n, err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, entry := range files {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > 16 {
			t.Fatalf("%s exceeded hard cap: %d", entry.Name(), info.Size())
		}
		total += info.Size()
	}
	if total > 48 {
		t.Fatalf("log family exceeded retention cap: %d", total)
	}
}

func TestRotatingFileRejectsMoreThanFiftyMiB(t *testing.T) {
	if _, err := NewRotatingFile(t.TempDir(), "too-large.log", 30*1024*1024, 1); err == nil {
		t.Fatal("expected log footprint over 50 MiB to be rejected")
	}
}

func TestRotatingFilePrunesOversizedExistingLogs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.log"), []byte(strings.Repeat("a", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.log.1"), []byte(strings.Repeat("b", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.log.9"), []byte("obsolete"), 0600); err != nil {
		t.Fatal(err)
	}
	writer, err := NewRotatingFile(dir, "app.log", 16, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app.log", "app.log.1"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err == nil && info.Size() > 16 {
			t.Fatalf("%s remained oversized: %d", name, info.Size())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "app.log.9")); !os.IsNotExist(err) {
		t.Fatalf("obsolete backup was not removed: %v", err)
	}
}
