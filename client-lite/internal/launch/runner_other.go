//go:build !windows

package launch

import "fmt"

type otherRunner struct{}

func DefaultRunner() Runner { return otherRunner{} }

func (otherRunner) Executable() (string, error) {
	return "", fmt.Errorf("应用启动仅支持 Windows")
}

func (otherRunner) Start([]string) error {
	return fmt.Errorf("应用启动仅支持 Windows")
}

func (otherRunner) CreateShortcut(ShortcutOptions) (string, error) {
	return "", fmt.Errorf("桌面快捷方式仅支持 Windows")
}
