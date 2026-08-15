package launch

import "easy-net/client-lite/internal/model"

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
