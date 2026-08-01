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
