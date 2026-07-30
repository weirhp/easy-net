//go:build !windows && !darwin

package tray

import (
	"fmt"
	"os/exec"
)

func Run(managerURL string, quit <-chan struct{}, requestQuit func()) {
	_ = OpenBrowser(managerURL)
	<-quit
}

func OpenBrowser(url string) error {
	if err := exec.Command("xdg-open", url).Start(); err != nil {
		return fmt.Errorf("打开浏览器：%w", err)
	}
	return nil
}
