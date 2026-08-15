//go:build !windows

package launch

import (
	"fmt"

	"easy-net/client-lite/internal/model"
)

type otherRunner struct{}

func DefaultRunner() Runner { return otherRunner{} }

func (otherRunner) Executable() (string, error) {
	return "", fmt.Errorf("应用启动仅支持 Windows")
}

func (otherRunner) Start([]string) error {
	return fmt.Errorf("应用启动仅支持 Windows")
}

func (otherRunner) IsRunning(model.LaunchEntry) (bool, error) { return false, nil }

func (otherRunner) CheckProxy(string) error { return fmt.Errorf("代理检查仅支持 Windows") }

func (otherRunner) Processes() ([]ProcessInfo, error) {
	return nil, fmt.Errorf("进程选择仅支持 Windows")
}

func (otherRunner) CreateShortcut(ShortcutOptions) (string, error) {
	return "", fmt.Errorf("桌面快捷方式仅支持 Windows")
}
