package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"sync"

	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/service"
	"easy-net/client-lite/internal/web"
)

type memorySecrets struct {
	mu     sync.Mutex
	values map[string]string
}

func (m *memorySecrets) Get(ref string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[ref]
	if !ok {
		return "", os.ErrNotExist
	}
	return value, nil
}
func (m *memorySecrets) Set(ref, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[ref] = value
	return nil
}
func (m *memorySecrets) Delete(ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, ref)
	return nil
}

func main() {
	dir, err := os.MkdirTemp("", "easy-net-lite-preview-")
	if err != nil {
		panic(err)
	}
	svc, err := service.New(config.NewStoreAt(dir+string(os.PathSeparator)+"config.json"), &memorySecrets{values: map[string]string{}})
	if err != nil {
		panic(err)
	}
	done := make(chan struct{})
	var once sync.Once
	manager, err := web.New(svc, func() { once.Do(func() { close(done) }) })
	if err != nil {
		panic(err)
	}
	server := httptest.NewServer(manager.Handler())
	fmt.Println("UI_URL=" + server.URL)
	<-done
	svc.StopAll()
	server.Close()
}
