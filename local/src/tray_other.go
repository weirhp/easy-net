//go:build !windows

package main

func RunTray(app *AppRuntime) {
	app.Wait()
}
