//go:build !windows

package clashsub

import (
	"fmt"
	"os"
	"path/filepath"
)

func sameExecutablePath(left, right string) bool {
	a, _ := filepath.Abs(left)
	b, _ := filepath.Abs(right)
	return a == b
}

func ownedProcessRunning(pid int, executable string) bool {
	target, err := os.Readlink(filepath.Join("/proc", fmt.Sprint(pid), "exe"))
	return err == nil && sameExecutablePath(target, executable)
}

func terminateOwnedProcess(pid int, executable string) error {
	if !ownedProcessRunning(pid, executable) {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
