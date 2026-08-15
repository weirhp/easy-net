//go:build !windows

package launch

func ConfirmRunningApplication(string) bool { return false }

func ShowLaunchError(string, string) {}
