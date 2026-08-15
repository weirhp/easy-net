package launch

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
	Executable() (string, error)
	CreateShortcut(options ShortcutOptions) (string, error)
}
