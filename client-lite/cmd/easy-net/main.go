package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/secretstore"
	"easy-net/client-lite/internal/service"
	"easy-net/client-lite/internal/tray"
	"easy-net/client-lite/internal/version"
	"easy-net/client-lite/internal/web"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
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

	quit := make(chan struct{})
	var quitOnce sync.Once
	requestQuit := func() { quitOnce.Do(func() { close(quit) }) }
	manager, err := web.New(svc, requestQuit)
	if err != nil {
		log.Fatal(err)
	}
	if err := manager.Start(); err != nil {
		log.Fatal(err)
	}
	log.Printf("[Easy-Net Lite %s] 管理界面：%s", version.Value, manager.URL())

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
			log.Printf("[Easy-Net Lite] 关闭管理服务：%v", err)
		}
		close(shutdownDone)
	}()

	tray.Run(manager.URL(), quit, requestQuit)
	requestQuit()
	<-shutdownDone
}

func configureLogging(configDir string) func() {
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return func() {}
	}
	path := configDir + string(os.PathSeparator) + "easy-net-lite.log"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return func() {}
	}
	log.SetOutput(file)
	return func() { _ = file.Close() }
}
