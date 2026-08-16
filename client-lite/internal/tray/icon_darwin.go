//go:build darwin

package tray

import _ "embed"

//go:embed easy-net-lite.png
var trayIcon []byte

func makeTrayIcon() []byte { return trayIcon }
