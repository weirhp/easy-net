//go:build !windows

package launch

import (
	"os"
	"path/filepath"
)

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}

func syncFileDirectory(path string) {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return
	}
	_ = directory.Sync()
	_ = directory.Close()
}
