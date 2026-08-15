//go:build windows

package launch

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"easy-net/client-lite/internal/model"
	"golang.org/x/sys/windows"
)

type windowsRunner struct{}

func DefaultRunner() Runner { return windowsRunner{} }

func (windowsRunner) Executable() (string, error) {
	if env := strings.TrimSpace(os.Getenv("EASY_NET_HOOK")); env != "" {
		if _, err := os.Stat(env); err != nil {
			return "", fmt.Errorf("EASY_NET_HOOK 指向的程序不可用：%w", err)
		}
		return env, nil
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位 Easy-Net Lite：%w", err)
	}
	candidate := filepath.Join(filepath.Dir(self), "easy-net-hook.exe")
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("未找到 easy-net-hook.exe。请把它和 Easy-Net Lite 放在同一目录，或设置 EASY_NET_HOOK")
	}
	return candidate, nil
}

func (runner windowsRunner) Start(args []string) error {
	hook, err := runner.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(hook, args...)
	cmd.Dir = filepath.Dir(hook)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS | windows.CREATE_NO_WINDOW,
	}
	var diagnostics bytes.Buffer
	cmd.Stdout = &diagnostics
	cmd.Stderr = &diagnostics
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 Easy-Net Hook：%w", err)
	}
	go func() {
		if waitErr := cmd.Wait(); waitErr != nil {
			message := strings.TrimSpace(diagnostics.String())
			if len(message) > 4096 {
				message = message[:4096] + "…"
			}
			if message == "" {
				log.Printf("[Easy-Net Lite] Easy-Net Hook 后台启动失败：%v", waitErr)
			} else {
				log.Printf("[Easy-Net Lite] Easy-Net Hook 后台启动失败：%v：%s", waitErr, message)
			}
		}
	}()
	return nil
}

func (windowsRunner) CheckProxy(address string) error { return checkSOCKS5Proxy(address) }

func (windowsRunner) IsRunning(entry model.LaunchEntry) (bool, error) {
	if entry.WeChatExisting {
		return false, nil
	}
	names := launchProcessNames(entry)
	if len(names) == 0 {
		return false, nil
	}
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[strings.ToLower(name)] = struct{}{}
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, fmt.Errorf("检查运行中的应用：%w", err)
	}
	defer windows.CloseHandle(snapshot)
	var process windows.ProcessEntry32
	process.Size = uint32(unsafe.Sizeof(process))
	if err := windows.Process32First(snapshot, &process); err != nil {
		return false, fmt.Errorf("读取运行中的应用：%w", err)
	}
	for {
		name := strings.ToLower(windows.UTF16ToString(process.ExeFile[:]))
		if _, ok := wanted[name]; ok {
			return true, nil
		}
		if err := windows.Process32Next(snapshot, &process); err != nil {
			if err == windows.ERROR_NO_MORE_FILES {
				return false, nil
			}
			return false, fmt.Errorf("读取运行中的应用：%w", err)
		}
	}
}

func launchProcessNames(entry model.LaunchEntry) []string {
	if entry.Path != "" {
		return []string{filepath.Base(entry.Path)}
	}
	switch entry.Mode {
	case model.LaunchModeChatGPT:
		return []string{"ChatGPT.exe"}
	case model.LaunchModeAntigravity:
		return []string{"Antigravity IDE.exe"}
	case model.LaunchModeCursor:
		return []string{"Cursor.exe"}
	case model.LaunchModeWeChat, model.LaunchModeWeChatWinDivert:
		return []string{"Weixin.exe", "WeChat.exe", "xwechat.exe"}
	default:
		return nil
	}
}

func (windowsRunner) CreateShortcut(options ShortcutOptions) (string, error) {
	name := safeShortcutName(options.Name)
	if name == "" || options.Target == "" {
		return "", fmt.Errorf("快捷方式参数不完整")
	}
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$desktop = [Environment]::GetFolderPath('Desktop')
$path = Join-Path $desktop (%s + '（代理）.lnk')
$target = %s
$iconPath = %s
if (%s) {
  try {
    $package = Get-AppxPackage OpenAI.Codex | Sort-Object Version -Descending | Select-Object -First 1
    if ($package) {
      $candidate = Join-Path $package.InstallLocation 'app\resources\icon-chatgpt.ico'
      if (Test-Path -LiteralPath $candidate -PathType Leaf) {
        $iconDir = Join-Path ([Environment]::GetFolderPath('ApplicationData')) 'Easy-Net Lite\icons'
        New-Item -ItemType Directory -Force -Path $iconDir | Out-Null
        $cachedIcon = Join-Path $iconDir 'chatgpt.ico'
        Copy-Item -LiteralPath $candidate -Destination $cachedIcon -Force
        $iconPath = $cachedIcon
      }
    }
  } catch {}
}
if (-not $iconPath -or -not (Test-Path -LiteralPath $iconPath -PathType Leaf)) { $iconPath = $target }
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($path)
$shortcut.TargetPath = $target
$shortcut.Arguments = %s
$shortcut.WorkingDirectory = %s
$shortcut.WindowStyle = 7
$shortcut.Description = %s
$shortcut.IconLocation = $iconPath
$shortcut.Save()
Write-Output $path
`, psQuote(name), psQuote(options.Target), psQuote(options.IconPath), psBool(options.UseChatGPTIcon), psQuote(options.Arguments), psQuote(options.WorkingDirectory), psQuote(options.Description))
	encoded := base64.StdEncoding.EncodeToString(utf16LE(script))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("创建桌面快捷方式失败：%w", err)
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", fmt.Errorf("创建桌面快捷方式失败：未返回路径")
	}
	return path, nil
}

func utf16LE(value string) []byte {
	encoded := utf16.Encode([]rune(value))
	out := make([]byte, len(encoded)*2)
	for i, unit := range encoded {
		out[i*2] = byte(unit)
		out[i*2+1] = byte(unit >> 8)
	}
	return out
}

func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func psBool(value bool) string {
	if value {
		return "$true"
	}
	return "$false"
}
