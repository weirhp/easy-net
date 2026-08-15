//go:build windows

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/logging"
	"easy-net/client-lite/internal/secretstore"
	"easy-net/client-lite/internal/service"
	"easy-net/client-lite/internal/tray"
	"easy-net/client-lite/internal/version"
	"easy-net/client-lite/internal/web"
)

const defaultListenAddress = "127.0.0.1:18082"

func main() {
	configDir := flag.String("config-dir", "", "引擎配置目录")
	listen := flag.String("listen", defaultListenAddress, "本机控制接口地址")
	statusFile := flag.String("status-file", "", "引擎状态文件")
	showVersion := flag.Bool("version", false, "显示版本")
	flag.Parse()
	if *showVersion {
		fmt.Printf("Easy-Net Engine %s\n", version.Value)
		return
	}

	dir, err := resolveConfigDir(*configDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Fatal(err)
	}
	if *statusFile == "" {
		*statusFile = filepath.Join(dir, "status.json")
	}

	owned, releaseMutex, err := acquireInstanceMutex()
	if err != nil {
		log.Fatal(err)
	}
	defer releaseMutex()
	if !owned {
		return
	}

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	closeLog := configureLogging(dir)
	defer closeLog()

	store := config.NewStoreAt(filepath.Join(dir, "config.json"))
	svc, err := service.New(store, secretstore.NewKeyringWithService("Easy-Net Hook Engine"))
	if err != nil {
		log.Fatal(err)
	}

	quit := make(chan struct{})
	var quitOnce sync.Once
	requestQuit := func() { quitOnce.Do(func() { close(quit) }) }
	manager, err := web.NewWithOptions(svc, requestQuit, web.Options{
		ListenAddress: *listen,
		StatusFile:    *statusFile,
		Application:   "easy-net-engine",
		DisableAssets: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := manager.Start(); err != nil {
		var alreadyRunning *web.AlreadyRunningError
		if errors.As(err, &alreadyRunning) {
			return
		}
		log.Fatal(err)
	}
	log.Printf("[Easy-Net Engine %s] 控制接口：%s", version.Value, manager.URL())

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	go func() {
		<-signals
		requestQuit()
	}()
	go svc.StartAuto()

	shutdownDone := make(chan struct{})
	go func() {
		<-quit
		svc.StopAll()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := manager.Shutdown(ctx); err != nil {
			log.Printf("[Easy-Net Engine] 关闭控制接口：%v", err)
		}
		_ = os.Remove(*statusFile)
		close(shutdownDone)
	}()

	tray.RunWithOptions(tray.Options{
		Title:   "Easy-Net 代理",
		Tooltip: "Easy-Net Hook 内置代理",
	}, quit, requestQuit)
	requestQuit()
	<-shutdownDone
}

func resolveConfigDir(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("获取本地配置目录：%w", err)
	}
	return filepath.Join(base, "EasyNetHook", "engine"), nil
}

func configureLogging(configDir string) func() {
	file, err := logging.NewRotatingFile(configDir, "easy-net-engine.log", logging.DefaultMaxSize, logging.DefaultBackups)
	if err != nil {
		return func() {}
	}
	log.SetOutput(file)
	return func() { _ = file.Close() }
}
