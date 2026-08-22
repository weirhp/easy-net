//go:build !windows

package clashsub

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type otherProcessControl struct {
	mu      sync.Mutex
	process *os.Process
}

func retainProcessControl(pid int) (retainedProcessControl, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return nil, err
	}
	return &otherProcessControl{process: process}, nil
}

func (c *otherProcessControl) Terminate() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.process == nil {
		return nil
	}
	return c.process.Kill()
}

func (c *otherProcessControl) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.process != nil {
		_ = c.process.Release()
		c.process = nil
	}
}

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

func waitOwnedProcessExit(pid int, executable string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for ownedProcessRunning(pid, executable) {
		if time.Now().After(deadline) {
			return fmt.Errorf("等待旧 mihomo（PID %d）退出超时", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}
