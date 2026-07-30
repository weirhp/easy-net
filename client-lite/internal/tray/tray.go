//go:build windows || darwin

package tray

import (
	"log"
	"sync"

	"github.com/getlantern/systray"
)

func Run(managerURL string, quit <-chan struct{}, requestQuit func()) {
	var exitOnce sync.Once
	exit := func() { exitOnce.Do(requestQuit) }
	go func() {
		<-quit
		systray.Quit()
	}()
	systray.Run(func() {
		systray.SetIcon(makeTrayIcon())
		systray.SetTitle("Easy-Net Lite")
		systray.SetTooltip("Easy-Net Lite")
		open := systray.AddMenuItem("打开管理界面", "在浏览器中打开 Easy-Net Lite")
		systray.AddSeparator()
		quitItem := systray.AddMenuItem("退出程序", "停止全部代理并退出")
		go func() {
			if err := OpenBrowser(managerURL); err != nil {
				log.Printf("[Easy-Net Lite] 打开浏览器失败：%v", err)
			}
			for {
				select {
				case <-open.ClickedCh:
					if err := OpenBrowser(managerURL); err != nil {
						log.Printf("[Easy-Net Lite] 打开浏览器失败：%v", err)
					}
				case <-quitItem.ClickedCh:
					exit()
					return
				case <-quit:
					return
				}
			}
		}()
	}, exit)
}
