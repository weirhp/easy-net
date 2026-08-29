//go:build !windows

package clashsub

import (
	"os"
	"path/filepath"
)

func replaceRuntimeFile(source, destination string) error { return os.Rename(source, destination) }

func syncRuntimeFileDirectory(path string) {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return
	}
	_ = directory.Sync()
	_ = directory.Close()
}
