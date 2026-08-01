//go:build !windows

package config

import "os"

func hardenFile(path string) error { return os.Chmod(path, 0600) }
