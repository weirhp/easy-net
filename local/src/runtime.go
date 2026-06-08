package main

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
)

type AppRuntime struct {
	manager *Manager
	server  *http.Server
	uiURL   string

	quitOnce sync.Once
	quit     chan struct{}
}

func NewAppRuntime(manager *Manager, server *http.Server, uiURL string) *AppRuntime {
	return &AppRuntime{
		manager: manager,
		server:  server,
		uiURL:   uiURL,
		quit:    make(chan struct{}),
	}
}

func (a *AppRuntime) RequestQuit() {
	a.quitOnce.Do(func() {
		log.Printf("[Easy-Net] exiting by user request")
		a.manager.Shutdown()

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := a.server.Shutdown(ctx); err != nil {
			log.Printf("[Easy-Net] http shutdown warning: %v", err)
		}
		close(a.quit)
	})
}

func (a *AppRuntime) Wait() {
	<-a.quit
}
