package clashsub

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"
)

type Runner interface {
	Start(subscriptionID string, listenPort int, proxy map[string]any) error
	Stop(subscriptionID string) error
	Running(subscriptionID string) bool
}

type mihomoRunner struct {
	configDir string
	mu        sync.Mutex
	commands  map[string]*exec.Cmd
}

func DefaultRunner(configDir string) Runner {
	return &mihomoRunner{configDir: configDir, commands: map[string]*exec.Cmd{}}
}

func (r *mihomoRunner) Start(subscriptionID string, listenPort int, proxy map[string]any) error {
	r.mu.Lock()
	if cmd := r.commands[subscriptionID]; cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		delete(r.commands, subscriptionID)
	}
	r.mu.Unlock()

	exe, err := findMihomo(r.configDir)
	if err != nil {
		return err
	}
	workDir := filepath.Join(r.configDir, "clash", subscriptionID)
	configPath := filepath.Join(workDir, "config.yaml")
	if err := WriteMihomoConfig(configPath, listenPort, proxy); err != nil {
		return err
	}
	cmd := exec.Command(exe, "-d", workDir, "-f", configPath)
	cmd.Dir = workDir
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 mihomo：%w", err)
	}
	r.mu.Lock()
	r.commands[subscriptionID] = cmd
	r.mu.Unlock()
	go func() {
		_ = cmd.Wait()
		r.mu.Lock()
		if r.commands[subscriptionID] == cmd {
			delete(r.commands, subscriptionID)
		}
		r.mu.Unlock()
	}()
	if err := waitPort("127.0.0.1:"+strconv.Itoa(listenPort), 8*time.Second); err != nil {
		_ = r.Stop(subscriptionID)
		return fmt.Errorf("mihomo 本地端口未就绪：%w", err)
	}
	return nil
}

func (r *mihomoRunner) Stop(subscriptionID string) error {
	r.mu.Lock()
	cmd := r.commands[subscriptionID]
	delete(r.commands, subscriptionID)
	r.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func (r *mihomoRunner) Running(subscriptionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	cmd := r.commands[subscriptionID]
	return cmd != nil && cmd.Process != nil
}

func findMihomo(configDir string) (string, error) {
	if env := os.Getenv("EASY_NET_MIHOMO"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
		return "", fmt.Errorf("EASY_NET_MIHOMO 指向的程序不可用")
	}
	self, err := os.Executable()
	if err != nil {
		self = ""
	}
	name := "mihomo"
	if runtime.GOOS == "windows" {
		name = "mihomo.exe"
	}
	candidates := []string{
		filepath.Join(configDir, "mihomo", name),
	}
	if self != "" {
		dir := filepath.Dir(self)
		candidates = append([]string{
			filepath.Join(dir, name),
			filepath.Join(dir, "mihomo", name),
		}, candidates...)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("未找到 mihomo。请把 %s 放到 Easy-Net Lite 同一目录，或设置 EASY_NET_MIHOMO", name)
}

func waitPort(address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		last = err
		time.Sleep(150 * time.Millisecond)
	}
	if last == nil {
		last = fmt.Errorf("timeout")
	}
	return last
}
