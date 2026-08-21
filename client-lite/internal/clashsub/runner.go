package clashsub

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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
	opMu      sync.Mutex
	mu        sync.Mutex
	commands  map[string]*exec.Cmd
}

type mihomoRuntime struct {
	PID        int       `json:"pid"`
	Executable string    `json:"executable"`
	ListenPort int       `json:"listenPort"`
	StartedAt  time.Time `json:"startedAt"`
}

func DefaultRunner(configDir string) Runner {
	return &mihomoRunner{configDir: configDir, commands: map[string]*exec.Cmd{}}
}

func (r *mihomoRunner) Start(subscriptionID string, listenPort int, proxy map[string]any) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	exe, err := findMihomo(r.configDir)
	if err != nil {
		return err
	}
	if err := r.stopOwned(subscriptionID, exe); err != nil {
		return fmt.Errorf("停止旧 mihomo：%w", err)
	}
	address := "127.0.0.1:" + strconv.Itoa(listenPort)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("本地端口 %d 已被其他程序占用；请停止占用程序后重试", listenPort)
	}
	_ = listener.Close()
	workDir := filepath.Join(r.configDir, "clash", subscriptionID)
	configPath := filepath.Join(workDir, "config.yaml")
	if err := WriteMihomoConfig(configPath, listenPort, proxy); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(workDir, "mihomo.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("创建 mihomo 日志：%w", err)
	}
	cmd := exec.Command(exe, "-d", workDir, "-f", configPath)
	cmd.Dir = workDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = withoutProxyEnv(os.Environ())
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("启动 mihomo：%w", err)
	}
	runtimeInfo := mihomoRuntime{PID: cmd.Process.Pid, Executable: exe, ListenPort: listenPort, StartedAt: time.Now()}
	if err := r.writeRuntime(subscriptionID, runtimeInfo); err != nil {
		_ = cmd.Process.Kill()
		_ = logFile.Close()
		return err
	}
	r.mu.Lock()
	r.commands[subscriptionID] = cmd
	r.mu.Unlock()
	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
		_ = logFile.Close()
		r.mu.Lock()
		if r.commands[subscriptionID] == cmd {
			delete(r.commands, subscriptionID)
			r.removeRuntime(subscriptionID, cmd.Process.Pid)
		}
		r.mu.Unlock()
	}()
	if err := waitStartedPort(address, 8*time.Second, exited); err != nil {
		_ = r.stopOwned(subscriptionID, exe)
		return fmt.Errorf("mihomo 本地端口未就绪：%w", err)
	}
	if err := probeNodeThroughSOCKS("127.0.0.1:" + strconv.Itoa(listenPort)); err != nil {
		detail := lastMihomoConnectError(filepath.Join(workDir, "mihomo.log"))
		_ = r.stopOwned(subscriptionID, exe)
		if detail != "" {
			return fmt.Errorf("节点握手失败：%s", detail)
		}
		return fmt.Errorf("节点已监听，但无法建立加密连接：%w", err)
	}
	return nil
}

func (r *mihomoRunner) Stop(subscriptionID string) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	exe, _ := findMihomo(r.configDir)
	return r.stopOwned(subscriptionID, exe)
}

func (r *mihomoRunner) Running(subscriptionID string) bool {
	r.mu.Lock()
	cmd := r.commands[subscriptionID]
	r.mu.Unlock()
	if cmd != nil && cmd.Process != nil && ownedProcessRunning(cmd.Process.Pid, cmd.Path) {
		return true
	}
	info, err := r.readRuntime(subscriptionID)
	return err == nil && ownedProcessRunning(info.PID, info.Executable)
}

func (r *mihomoRunner) stopOwned(subscriptionID, expectedExecutable string) error {
	_ = expectedExecutable
	r.mu.Lock()
	cmd := r.commands[subscriptionID]
	delete(r.commands, subscriptionID)
	r.mu.Unlock()
	var firstErr error
	if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil && ownedProcessRunning(cmd.Process.Pid, cmd.Path) {
			firstErr = err
		}
	}
	info, err := r.readRuntime(subscriptionID)
	if err == nil && info.PID > 0 {
		ownedExecutable := info.Executable
		if ownedProcessRunning(info.PID, ownedExecutable) {
			if err := terminateOwnedProcess(info.PID, ownedExecutable); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	r.removeRuntime(subscriptionID, 0)
	return firstErr
}

func (r *mihomoRunner) runtimePath(subscriptionID string) string {
	return filepath.Join(r.configDir, "clash", subscriptionID, "runtime.json")
}

func (r *mihomoRunner) writeRuntime(subscriptionID string, info mihomoRuntime) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("记录 mihomo 进程：%w", err)
	}
	path := r.runtimePath(subscriptionID)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0600); err != nil {
		return err
	}
	if err := replaceRuntimeFile(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (r *mihomoRunner) readRuntime(subscriptionID string) (mihomoRuntime, error) {
	data, err := os.ReadFile(r.runtimePath(subscriptionID))
	if err != nil {
		return mihomoRuntime{}, err
	}
	var info mihomoRuntime
	if err := json.Unmarshal(data, &info); err != nil {
		return mihomoRuntime{}, err
	}
	if info.PID <= 0 || info.Executable == "" {
		return mihomoRuntime{}, fmt.Errorf("mihomo 运行记录无效")
	}
	return info, nil
}

func (r *mihomoRunner) removeRuntime(subscriptionID string, expectedPID int) {
	if expectedPID > 0 {
		info, err := r.readRuntime(subscriptionID)
		if err == nil && info.PID != expectedPID {
			return
		}
	}
	_ = os.Remove(r.runtimePath(subscriptionID))
}

func findMihomo(configDir string) (string, error) {
	if env := os.Getenv("EASY_NET_MIHOMO"); env != "" {
		if _, err := os.Stat(env); err == nil {
			absolute, absoluteErr := filepath.Abs(env)
			if absoluteErr != nil {
				return "", fmt.Errorf("解析 EASY_NET_MIHOMO：%w", absoluteErr)
			}
			return absolute, nil
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
			absolute, absoluteErr := filepath.Abs(candidate)
			if absoluteErr != nil {
				continue
			}
			return absolute, nil
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

func waitStartedPort(address string, timeout time.Duration, exited <-chan error) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	var last error
	for {
		select {
		case err := <-exited:
			if err == nil {
				err = fmt.Errorf("mihomo 已退出")
			}
			return err
		case <-deadline.C:
			if last == nil {
				last = fmt.Errorf("timeout")
			}
			return last
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", address, 300*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return nil
			}
			last = err
		}
	}
}

func withoutProxyEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, item := range environ {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "http_proxy", "https_proxy", "all_proxy", "no_proxy", "ftp_proxy":
			continue
		}
		out = append(out, item)
	}
	return out
}
