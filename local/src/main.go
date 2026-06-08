package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	workDir := appDir()
	store := NewConfigStore(workDir)
	cfg, err := store.Load()
	if err != nil {
		log.Fatalf("[Easy-Net] failed to load config: %v", err)
	}

	manager := NewManager(workDir, store, cfg)
	if err := manager.ApplyStartup(); err != nil {
		log.Printf("[Easy-Net] startup apply warning: %v", err)
	}

	mux := http.NewServeMux()
	addr := "127.0.0.1:" + strconv.Itoa(cfg.ManagerPort)
	server := &http.Server{Addr: addr, Handler: mux}
	app := NewAppRuntime(manager, server, "http://"+addr)
	manager.RegisterRoutes(mux, app)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	go func() {
		<-signals
		app.RequestQuit()
	}()

	log.Printf("=================================================")
	log.Printf("[Easy-Net] local manager started")
	log.Printf("UI: http://%s", addr)
	log.Printf("Config: %s", store.Path())
	log.Printf("=================================================")

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[Easy-Net] manager stopped: %v", err)
		}
	}()

	RunTray(app)
}

func appDir() string {
	if exePath, err := os.Executable(); err == nil {
		dir := filepath.Dir(exePath)
		if filepath.Base(dir) == "dist" {
			parent := filepath.Dir(dir)
			if isAppDir(parent) {
				return parent
			}
		}
		if isAppDir(dir) {
			return dir
		}
		if isAppDir(filepath.Dir(dir)) {
			return filepath.Dir(dir)
		}
	}
	if wd, err := os.Getwd(); err == nil {
		if filepath.Base(wd) == "scripts" || filepath.Base(wd) == "dist" {
			parent := filepath.Dir(wd)
			if isAppDir(parent) {
				return parent
			}
		}
		if isAppDir(wd) {
			return wd
		}
		return wd
	}
	return "."
}

func isAppDir(dir string) bool {
	if dir == "" || dir == "." {
		return false
	}
	for _, name := range []string{"go.mod", "local-config.json", "local-config.json.example"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}
