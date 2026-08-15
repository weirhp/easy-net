//go:build windows

package launch

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var messageBoxW = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

const (
	messageBoxYesNo      = 0x00000004
	messageBoxIconWarn   = 0x00000030
	messageBoxDefaultTwo = 0x00000100
	messageBoxResultYes  = 6
)

func ConfirmRunningApplication(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "这个应用"
	}
	message, _ := windows.UTF16PtrFromString("检测到“" + name + "”已经在运行。\r\n\r\n再次启动可能打开新窗口，也可能由现有进程接管。是否仍然启动？")
	title, _ := windows.UTF16PtrFromString("应用正在运行")
	result, _, _ := messageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(message)),
		uintptr(unsafe.Pointer(title)),
		uintptr(messageBoxYesNo|messageBoxIconWarn|messageBoxDefaultTwo),
	)
	return result == messageBoxResultYes
}

func ShowLaunchError(title, message string) {
	if strings.TrimSpace(title) == "" {
		title = "启动失败"
	}
	messageText, _ := windows.UTF16PtrFromString(message)
	titleText, _ := windows.UTF16PtrFromString(title)
	messageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(messageText)),
		uintptr(unsafe.Pointer(titleText)),
		0x00000010,
	)
}
