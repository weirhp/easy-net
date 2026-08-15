//go:build windows || darwin

package tray

import (
	"log"
	"sync"

	"github.com/getlantern/systray"
)

func Run(managerURL string, quit <-chan struct{}, requestQuit func()) {
	RunWithOptions(Options{
		Title:         "Easy-Net Lite",
		Tooltip:       "Easy-Net Lite",
		OpenURL:       managerURL,
		OpenMenuLabel: "打开管理界面",
		OpenMenuHelp:  "在浏览器中打开 Easy-Net Lite",
	}, quit, requestQuit)
}

func RunWithOptions(options Options, quit <-chan struct{}, requestQuit func()) {
	var exitOnce sync.Once
	exit := func() { exitOnce.Do(requestQuit) }
	go func() {
		<-quit
		systray.Quit()
	}()
	title := options.Title
	if title == "" {
		title = "Easy-Net Lite"
	}
	tooltip := options.Tooltip
	if tooltip == "" {
		tooltip = title
	}
	systray.Run(func() {
		systray.SetIcon(makeTrayIcon())
		systray.SetTitle(title)
		systray.SetTooltip(tooltip)
		var open *systray.MenuItem
		if options.OpenURL != "" {
			label := options.OpenMenuLabel
			if label == "" {
				label = "打开管理界面"
			}
			help := options.OpenMenuHelp
			if help == "" {
				help = label
			}
			open = systray.AddMenuItem(label, help)
			systray.AddSeparator()
		}
		quitItem := systray.AddMenuItem("退出程序", "停止全部代理并退出")
		go func() {
			if options.OpenURL != "" {
				if err := OpenBrowser(options.OpenURL); err != nil {
					log.Printf("[Easy-Net Lite] 打开浏览器失败：%v", err)
				}
			}
			for {
				select {
				case <-clicked(open):
					if err := OpenBrowser(options.OpenURL); err != nil {
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

func clicked(item *systray.MenuItem) <-chan struct{} {
	if item == nil {
		return nil
	}
	return item.ClickedCh
}
