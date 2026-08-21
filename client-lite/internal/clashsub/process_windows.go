//go:build windows

package clashsub

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

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
