//go:build linux

package clashsub

import (
	"fmt"
	"os"
	"path/filepath"
)

func ownedProcessRunning(pid int, executable string) bool {
	if pid <= 0 || executable == "" {
		return false
	}
	target, err := os.Readlink(filepath.Join("/proc", fmt.Sprint(pid), "exe"))
	return err == nil && sameExecutablePath(target, executable)
}
