//go:build !windows && !darwin

package tray

import (
	"fmt"
	"os/exec"
)

func Run(managerURL string, quit <-chan struct{}, requestQuit func()) {
	RunWithOptions(Options{OpenURL: managerURL}, quit, requestQuit)
}

func RunWithOptions(options Options, quit <-chan struct{}, requestQuit func()) {
	_ = requestQuit
	if options.OpenURL != "" && !options.SkipInitialOpen {
		_ = OpenBrowser(options.OpenURL)
	}
	<-quit
}

func OpenBrowser(url string) error {
	if err := exec.Command("xdg-open", url).Start(); err != nil {
		return fmt.Errorf("打开浏览器：%w", err)
	}
	return nil
}
