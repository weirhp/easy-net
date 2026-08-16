//go:build windows

package tray

import _ "embed"

//go:embed easy-net-lite.ico
var trayIcon []byte

func makeTrayIcon() []byte { return trayIcon }
