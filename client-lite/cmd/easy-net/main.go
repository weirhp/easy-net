package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/launch"
	"easy-net/client-lite/internal/logging"
	"easy-net/client-lite/internal/secretstore"
	"easy-net/client-lite/internal/service"
	"easy-net/client-lite/internal/tray"
	"easy-net/client-lite/internal/version"
	"easy-net/client-lite/internal/web"
)

func main() {
	launchID, openApps, background, showVersion := parseArgs(os.Args[1:])
	if showVersion {
		fmt.Printf("Easy-Net Lite %s\n", version.Value)
		return
	}
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	store, err := config.NewStore()
	if err != nil {
		log.Fatal(err)
	}
	closeLog := configureLogging(store.Dir())
	defer closeLog()
	svc, err := service.New(store, secretstore.NewKeyring())
	if err != nil {
		log.Fatal(err)
	}
	launches, err := launch.New(store.Dir(), svc, nil)
	if err != nil {
		log.Fatal(err)
	}

	quit := make(chan struct{})
	var quitOnce sync.Once
	requestQuit := func() { quitOnce.Do(func() { close(quit) }) }
	statusPath := filepath.Join(store.Dir(), "status.json")
	manager, err := web.NewWithOptions(svc, requestQuit, web.Options{Launches: launches, StatusFile: statusPath})
	if err != nil {
		log.Fatal(err)
	}
	if err := manager.Start(); err != nil {
		var alreadyRunning *web.AlreadyRunningError
		if errors.As(err, &alreadyRunning) {
			if launchID != "" {
				if startErr := launch.StartOnExisting(alreadyRunning.URL, launchID); startErr != nil {
					log.Printf("[Easy-Net Lite] 启动已保存的应用失败：%v", startErr)
				}
				return
			}
			url := alreadyRunning.URL
			if openApps {
				url = launch.AppsURL(url)
			}
			if openErr := tray.OpenBrowser(url); openErr != nil {
				log.Printf("[Easy-Net Lite] 打开已运行的管理界面失败：%v", openErr)
			}
			return
		}
		log.Fatal(err)
	}
	defer os.Remove(statusPath)
	log.Printf("[Easy-Net Lite %s] 管理界面：%s", version.Value, manager.URL())

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	go func() {
		<-signals
		requestQuit()
	}()
	go svc.StartAuto()
	if launchID != "" {
		go func() {
			_, startErr := launches.Start(launchID)
			var running *launch.AlreadyRunningError
			if errors.As(startErr, &running) && launch.ConfirmRunningApplication(running.Entry.Name) {
				_, startErr = launches.StartWithOptions(launchID, launch.StartOptions{ConfirmRunning: true})
			} else if errors.As(startErr, &running) {
				startErr = nil
			}
			if startErr != nil {
				title := "启动失败"
				var unavailable *launch.ProxyUnavailableError
				if errors.As(startErr, &unavailable) {
					title = "代理不可用"
				}
				launch.ShowLaunchError(title, startErr.Error())
				log.Printf("[Easy-Net Lite] 启动入口失败：%v", startErr)
			}
		}()
	}

	shutdownDone := make(chan struct{})
	go func() {
		<-quit
		svc.StopAll()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := manager.Shutdown(ctx); err != nil {
			log.Printf("[Easy-Net Lite] 关闭管理服务：%v", err)
		}
		close(shutdownDone)
	}()

	openURL := manager.URL()
	if openApps {
		openURL = launch.AppsURL(openURL)
	}
	tray.RunWithOptions(tray.Options{
		OpenURL:         openURL,
		SkipInitialOpen: background || launchID != "" && !openApps,
	}, quit, requestQuit)
	requestQuit()
	<-shutdownDone
}

func parseArgs(args []string) (launchID string, openApps bool, background bool, showVersion bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--version", "-version":
			showVersion = true
		case "--open-apps":
			openApps = true
		case "--background":
			background = true
		case "--launch-entry":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				log.Fatal("--launch-entry 需要启动入口 ID")
			}
			launchID = strings.TrimSpace(args[i+1])
			i++
		}
	}
	return launchID, openApps, background, showVersion
}

func configureLogging(configDir string) func() {
	file, err := logging.NewRotatingFile(configDir, "easy-net-lite.log", logging.DefaultMaxSize, logging.DefaultBackups)
	if err != nil {
		return func() {}
	}
	log.SetOutput(file)
	return func() { _ = file.Close() }
}
