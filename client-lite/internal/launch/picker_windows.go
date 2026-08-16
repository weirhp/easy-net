//go:build windows

package launch

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type PickedApplication struct {
	Source    string `json:"source"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Arguments string `json:"arguments,omitempty"`
}

func pickApplicationFiles(kind string) ([]PickedApplication, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "exe" && kind != "shortcut" {
		return nil, fmt.Errorf("文件类型无效")
	}
	filter := "可执行文件 (*.exe)|*.exe"
	if kind == "shortcut" {
		filter = "Windows 快捷方式 (*.lnk)|*.lnk"
	}
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.OpenFileDialog
$dialog.Title = %s
$dialog.Filter = %s
$dialog.Multiselect = $true
$dialog.CheckFileExists = $true
$dialog.RestoreDirectory = $true
$dialog.InitialDirectory = [Environment]::GetFolderPath('Desktop')
$owner = New-Object System.Windows.Forms.Form
$owner.ShowInTaskbar = $false
$owner.StartPosition = 'CenterScreen'
$owner.Size = New-Object System.Drawing.Size(1, 1)
$owner.Opacity = 0.01
$owner.TopMost = $true
$owner.Show()
$owner.Activate()
try {
  $result = $dialog.ShowDialog($owner)
} finally {
  $owner.Close()
  $owner.Dispose()
}
if ($result -ne [System.Windows.Forms.DialogResult]::OK) { exit 0 }
$shell = New-Object -ComObject WScript.Shell
$items = @()
foreach ($source in $dialog.FileNames) {
  $target = $source
  $arguments = ''
  $displayName = [IO.Path]::GetFileName($source)
  if ([IO.Path]::GetExtension($source) -ieq '.lnk') {
    $shortcut = $shell.CreateShortcut($source)
    $target = [Environment]::ExpandEnvironmentVariables($shortcut.TargetPath)
    $arguments = $shortcut.Arguments
    if ($target -and [IO.Path]::GetExtension($target) -ieq '.exe') {
      $displayName = [IO.Path]::GetFileName($target)
    } else {
      $target = ''
      $displayName = [IO.Path]::GetFileNameWithoutExtension($source)
    }
  }
  if ($target -and ([IO.Path]::GetExtension($target) -ine '.exe' -or -not (Test-Path -LiteralPath $target -PathType Leaf))) { continue }
  $items += [pscustomobject]@{ source = $source; name = $displayName; path = $target; arguments = $arguments }
}
ConvertTo-Json -InputObject @($items) -Compress
`, psQuote("选择要代理的应用"), psQuote(filter))
	encoded := base64.StdEncoding.EncodeToString(utf16LE(script))
	command := exec.Command("powershell.exe", "-NoProfile", "-STA", "-NonInteractive", "-WindowStyle", "Hidden", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 1000 {
			detail = detail[:1000] + "..."
		}
		if detail != "" {
			return nil, fmt.Errorf("打开文件选择器失败：%s", detail)
		}
		return nil, fmt.Errorf("打开文件选择器失败：%w", err)
	}
	if strings.TrimSpace(string(output)) == "" {
		return []PickedApplication{}, nil
	}
	var applications []PickedApplication
	if err := json.Unmarshal(output, &applications); err != nil {
		return nil, fmt.Errorf("读取所选应用失败：%w", err)
	}
	return applications, nil
}
