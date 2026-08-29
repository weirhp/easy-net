//go:build darwin

package clashsub

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func ownedProcessRunning(pid int, executable string) bool {
	if pid <= 0 || executable == "" {
		return false
	}
	// macOS has no /proc filesystem. ps(1)'s comm field reports the executable
	// used by the live PID without including its arguments.
	output, err := exec.Command("/bin/ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false
	}
	target := strings.TrimSpace(string(output))
	if target == "" {
		return false
	}
	if sameExecutablePath(target, executable) {
		return true
	}
	// Some macOS ps versions return only argv[0]. Restrict the fallback to the
	// exact basename so a recycled PID for a different process is not killed.
	return filepath.Base(target) == filepath.Base(executable)
}
