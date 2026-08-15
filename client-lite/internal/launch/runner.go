package launch

import (
	"fmt"
	"strings"

	"easy-net/client-lite/internal/model"
)

type HookStartError struct {
	ExitCode    int
	Diagnostics string
	Cause       error
}

func (e *HookStartError) Error() string {
	detail := strings.TrimSpace(e.Diagnostics)
	if detail != "" {
		return detail
	}
	if e.ExitCode >= 0 {
		return fmt.Sprintf("Easy-Net Hook 返回错误代码 %d", e.ExitCode)
	}
	return fmt.Sprintf("Easy-Net Hook 启动失败：%v", e.Cause)
}

func (e *HookStartError) Unwrap() error { return e.Cause }

type ShortcutOptions struct {
	Name             string
	Target           string
	Arguments        string
	WorkingDirectory string
	Description      string
	IconPath         string
	UseChatGPTIcon   bool
}

type ProcessInfo struct {
	PID  uint32 `json:"pid"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type Runner interface {
	Start(args []string) error
	IsRunning(entry model.LaunchEntry) (bool, error)
	CheckProxy(address string) error
	Processes() ([]ProcessInfo, error)
	Executable() (string, error)
	CreateShortcut(options ShortcutOptions) (string, error)
}
