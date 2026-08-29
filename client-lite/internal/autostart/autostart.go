package autostart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const valueName = "Easy-Net Lite"

// Status describes the per-user login startup entry.
type Status struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Current   bool   `json:"current"`
	Error     string `json:"error,omitempty"`
}

// Manager manages the login startup entry for this executable.
type Manager struct {
	executable string
	initErr    error
}

func New() *Manager {
	executable, err := os.Executable()
	if err == nil {
		executable, err = filepath.Abs(executable)
	}
	return &Manager{executable: filepath.Clean(executable), initErr: err}
}

func (m *Manager) Status() Status {
	status := Status{Supported: platformSupported()}
	if !status.Supported {
		return status
	}
	if m == nil || m.initErr != nil || strings.TrimSpace(m.executable) == "" {
		status.Error = "无法确定 Easy-Net Lite 程序路径"
		if m != nil && m.initErr != nil {
			status.Error = fmt.Sprintf("无法确定 Easy-Net Lite 程序路径：%v", m.initErr)
		}
		return status
	}
	command, exists, err := readPlatformEntry(valueName)
	if err != nil {
		status.Error = fmt.Sprintf("读取开机启动设置：%v", err)
		return status
	}
	status.Enabled = exists
	status.Current = exists && strings.EqualFold(strings.TrimSpace(command), m.command())
	return status
}

func (m *Manager) SetEnabled(enabled bool) error {
	if !platformSupported() {
		return fmt.Errorf("当前系统暂不支持管理开机启动")
	}
	if m == nil || m.initErr != nil || strings.TrimSpace(m.executable) == "" {
		return fmt.Errorf("无法确定 Easy-Net Lite 程序路径")
	}
	if enabled {
		if err := writePlatformEntry(valueName, m.command()); err != nil {
			return fmt.Errorf("启用开机启动：%w", err)
		}
		return nil
	}
	if err := deletePlatformEntry(valueName); err != nil {
		return fmt.Errorf("关闭开机启动：%w", err)
	}
	return nil
}

// RepairIfEnabled updates an existing entry after the application is moved.
// It never enables startup without an existing user choice.
func (m *Manager) RepairIfEnabled() error {
	if !platformSupported() {
		return nil
	}
	command, exists, err := readPlatformEntry(valueName)
	if err != nil {
		return fmt.Errorf("读取开机启动设置：%w", err)
	}
	if !exists {
		return nil
	}
	if m == nil || m.initErr != nil || strings.TrimSpace(m.executable) == "" {
		return fmt.Errorf("无法确定 Easy-Net Lite 程序路径")
	}
	if strings.EqualFold(strings.TrimSpace(command), m.command()) {
		return nil
	}
	return m.SetEnabled(true)
}

func (m *Manager) command() string {
	// Windows executable paths cannot contain a double quote. Quoting the whole
	// path keeps spaces safe without applying Go string escaping to backslashes.
	return `"` + strings.ReplaceAll(m.executable, `"`, "") + `" --background`
}
