package launch

import (
	"fmt"
	"strings"

	"easy-net/client-lite/internal/model"
)

func HookArgs(entry model.LaunchEntry, proxy string) ([]string, error) {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return nil, fmt.Errorf("本地代理地址为空")
	}
	entry.Normalize()
	if err := entry.ValidateForStart(); err != nil {
		return nil, err
	}
	args := []string{"--proxy", proxy, "--detach", "--gui-worker"}
	switch entry.Mode {
	case model.LaunchModeChatGPT:
		args = append(args, "--chatgpt-app")
	case model.LaunchModeAntigravity:
		args = append(args, "--antigravity")
		if entry.Isolated {
			args = append(args, "--antigravity-isolated")
		}
		if entry.Path != "" {
			args = append(args, "--antigravity-path", entry.Path)
		}
		if entry.DNS != "" {
			args = append(args, "--dns", entry.DNS)
		}
		if entry.Arguments != "" {
			args = append(args, "--")
			args = append(args, splitArguments(entry.Arguments)...)
		}
	case model.LaunchModeCursor:
		args = append(args, "--cursor")
		if entry.Isolated {
			args = append(args, "--cursor-isolated")
		}
		if entry.Path != "" {
			args = append(args, "--cursor-path", entry.Path)
		}
		if entry.Arguments != "" {
			args = append(args, "--")
			args = append(args, splitArguments(entry.Arguments)...)
		}
	case model.LaunchModeClaude:
		if entry.Path == "" {
			return nil, fmt.Errorf("Claude Code 需要填写可执行文件路径")
		}
		if entry.DNS != "" {
			args = append(args, "--dns", entry.DNS)
		}
		args = append(args, "--", entry.Path)
		if entry.Arguments != "" {
			args = append(args, splitArguments(entry.Arguments)...)
		}
	case model.LaunchModeChrome, model.LaunchModeEdge:
		if entry.AttachExisting {
			args = append(args, "--windivert", "--windivert-existing", "--udp-mode", entry.UDPMode)
			processName := "chrome.exe"
			if entry.Mode == model.LaunchModeEdge {
				processName = "msedge.exe"
			}
			if entry.ProcessNames != "" {
				processName += ";" + entry.ProcessNames
			}
			args = append(args, "--windivert-processes", processName)
			break
		}
		if entry.Mode == model.LaunchModeChrome {
			args = append(args, "--chrome")
		} else {
			args = append(args, "--edge")
		}
		if entry.Path != "" {
			args = append(args, "--browser-path", entry.Path)
		}
		if entry.Isolated {
			args = append(args, "--browser-isolated")
		}
		if entry.Arguments != "" {
			args = append(args, "--")
			args = append(args, splitArguments(entry.Arguments)...)
		}
	case model.LaunchModeWeChat, model.LaunchModeWeChatWinDivert:
		if entry.WeChatExisting {
			args = append(args, "--wechat-existing")
		} else {
			args = append(args, "--wechat")
		}
		udp := entry.UDPMode
		if udp == "" {
			udp = "auto"
		}
		args = append(args, "--udp-mode", udp)
		if !entry.WeChatExisting && entry.Path != "" {
			args = append(args, "--wechat-path", entry.Path)
		}
		if !entry.WeChatExisting && entry.Arguments != "" {
			args = append(args, "--")
			args = append(args, splitArguments(entry.Arguments)...)
		}
	case model.LaunchModeHook:
		if entry.DNS != "" {
			args = append(args, "--dns", entry.DNS)
		}
		args = append(args, "--", entry.Path)
		if entry.Arguments != "" {
			args = append(args, splitArguments(entry.Arguments)...)
		}
	case model.LaunchModeWinDivert:
		args = append(args, "--windivert", "--udp-mode", entry.UDPMode)
		if entry.AttachExisting {
			args = append(args, "--windivert-existing")
		}
		if entry.ProcessNames != "" {
			args = append(args, "--windivert-processes", entry.ProcessNames)
		}
		if !entry.AttachExisting {
			args = append(args, "--", entry.Path)
			if entry.Arguments != "" {
				args = append(args, splitArguments(entry.Arguments)...)
			}
		}
	default:
		return nil, fmt.Errorf("不支持的启动场景：%s", entry.Mode)
	}
	return args, nil
}

func splitArguments(value string) []string {
	// Follow the Windows command-line backslash/quote rules closely enough for
	// CommandLineToArgvW-style input.  A backslash is ordinary unless it appears
	// immediately before a quote; treating every backslash as an escape corrupts
	// common arguments such as C:\Users\name\file.txt.
	var args []string
	var current strings.Builder
	runes := []rune(value)
	for index := 0; index < len(runes); {
		for index < len(runes) && (runes[index] == ' ' || runes[index] == '\t') {
			index++
		}
		if index >= len(runes) {
			break
		}
		current.Reset()
		inQuotes := false
		started := false
		for index < len(runes) {
			if (runes[index] == ' ' || runes[index] == '\t') && !inQuotes {
				break
			}
			if runes[index] == '\\' {
				start := index
				for index < len(runes) && runes[index] == '\\' {
					index++
				}
				count := index - start
				if index < len(runes) && runes[index] == '"' {
					current.WriteString(strings.Repeat("\\", count/2))
					if count%2 == 1 {
						current.WriteRune('"')
					} else {
						inQuotes = !inQuotes
					}
					started = true
					index++
					continue
				}
				current.WriteString(strings.Repeat("\\", count))
				started = true
				continue
			}
			if runes[index] == '"' {
				inQuotes = !inQuotes
				started = true
				index++
				continue
			}
			current.WriteRune(runes[index])
			started = true
			index++
		}
		if started {
			args = append(args, current.String())
		}
	}
	return args
}
