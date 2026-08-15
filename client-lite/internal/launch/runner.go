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

type Runner interface {
	Start(args []string) error
	IsRunning(entry model.LaunchEntry) (bool, error)
	CheckProxy(address string) error
	Executable() (string, error)
	CreateShortcut(options ShortcutOptions) (string, error)
}
