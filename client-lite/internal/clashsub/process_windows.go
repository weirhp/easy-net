//go:build windows

package clashsub

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

type windowsProcessControl struct {
	mu     sync.Mutex
	handle windows.Handle
}

func retainProcessControl(pid int) (retainedProcessControl, error) {
	handle, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid),
	)
	if err != nil {
		return nil, err
	}
	return &windowsProcessControl{handle: handle}, nil
}

func (c *windowsProcessControl) Terminate() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handle == 0 {
		return nil
	}
	if result, _ := windows.WaitForSingleObject(c.handle, 0); result != uint32(windows.WAIT_TIMEOUT) {
		return nil
	}
	if err := windows.TerminateProcess(c.handle, 0); err != nil {
		// ACCESS_DENIED is also returned when exit wins the race. Only suppress it
		// after the retained handle confirms that the process has finished.
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			if result, _ := windows.WaitForSingleObject(c.handle, 0); result != uint32(windows.WAIT_TIMEOUT) {
				return nil
			}
		}
		return err
	}
	return nil
}

func (c *windowsProcessControl) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handle != 0 {
		_ = windows.CloseHandle(c.handle)
		c.handle = 0
	}
}

func sameExecutablePath(left, right string) bool {
	a, errA := filepath.Abs(left)
	b, errB := filepath.Abs(right)
	if errA == nil {
		left = a
	}
	if errB == nil {
		right = b
	}
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func ownedProcessRunning(pid int, executable string) bool {
	if pid <= 0 || executable == "" {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	if result, _ := windows.WaitForSingleObject(handle, 0); result != uint32(windows.WAIT_TIMEOUT) {
		return false
	}
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size) != nil {
		return false
	}
	return sameExecutablePath(windows.UTF16ToString(buffer[:size]), executable)
}

func terminateOwnedProcess(pid int, executable string) error {
	if !ownedProcessRunning(pid, executable) {
		return nil
	}
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	if err := windows.TerminateProcess(handle, 0); err != nil {
		return err
	}
	_, _ = windows.WaitForSingleObject(handle, 5000)
	return nil
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
