//go:build !windows

package clashsub

import "os"

func replaceRuntimeFile(source, destination string) error { return os.Rename(source, destination) }
