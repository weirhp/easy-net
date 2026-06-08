//go:build windows

package main

import (
	"encoding/binary"
	"log"
	"os/exec"

	"github.com/getlantern/systray"
)

func RunTray(app *AppRuntime) {
	go func() {
		app.Wait()
		systray.Quit()
	}()

	systray.Run(func() {
		systray.SetIcon(makeTrayIcon())
		systray.SetTooltip("Easy-Net Manager")

		openUI := systray.AddMenuItem("打开管理界面", "打开 Easy-Net Manager")
		systray.AddSeparator()
		quit := systray.AddMenuItem("退出程序", "停止代理并退出 Easy-Net Manager")

		go showStartupNotification(app.uiURL)

		go func() {
			for {
				select {
				case <-openUI.ClickedCh:
					if err := openBrowser(app.uiURL); err != nil {
						log.Printf("[Easy-Net] open browser warning: %v", err)
					}
				case <-quit.ClickedCh:
					app.RequestQuit()
					return
				}
			}
		}()
	}, func() {
		app.RequestQuit()
	})
}

func openBrowser(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func showStartupNotification(uiURL string) {
	script := `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$notify = New-Object System.Windows.Forms.NotifyIcon
$notify.Icon = [System.Drawing.SystemIcons]::Information
$notify.BalloonTipTitle = 'Easy-Net 已在后台运行'
$notify.BalloonTipText = '右键右下角托盘图标可打开管理界面或退出程序。管理地址：' + $args[0]
$notify.Visible = $true
$notify.ShowBalloonTip(8000)
Start-Sleep -Seconds 9
$notify.Dispose()
`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script, uiURL)
	if err := cmd.Start(); err != nil {
		log.Printf("[Easy-Net] startup notification warning: %v", err)
	}
}

func makeTrayIcon() []byte {
	const width = 16
	const height = 16
	const xorSize = width * height * 4
	const maskStride = 4
	const maskSize = maskStride * height
	const dibSize = 40 + xorSize + maskSize
	const fileSize = 6 + 16 + dibSize

	data := make([]byte, fileSize)
	binary.LittleEndian.PutUint16(data[2:4], 1)
	binary.LittleEndian.PutUint16(data[4:6], 1)
	data[6] = width
	data[7] = height
	binary.LittleEndian.PutUint16(data[10:12], 1)
	binary.LittleEndian.PutUint16(data[12:14], 32)
	binary.LittleEndian.PutUint32(data[14:18], dibSize)
	binary.LittleEndian.PutUint32(data[18:22], 22)

	dib := data[22:]
	binary.LittleEndian.PutUint32(dib[0:4], 40)
	binary.LittleEndian.PutUint32(dib[4:8], width)
	binary.LittleEndian.PutUint32(dib[8:12], height*2)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	binary.LittleEndian.PutUint32(dib[20:24], xorSize)

	pixels := dib[40 : 40+xorSize]
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := ((height-1-y)*width + x) * 4
			pixels[i] = byte(120 + y*5)
			pixels[i+1] = byte(95 + x*7)
			pixels[i+2] = byte(20 + y*4)
			pixels[i+3] = 255
			if x > 3 && x < 12 && y > 3 && y < 12 {
				pixels[i] = 245
				pixels[i+1] = 255
				pixels[i+2] = 255
			}
		}
	}
	return data
}
