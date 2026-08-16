package launch

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"easy-net/client-lite/internal/model"
)

func encodeShortcutSpec(entry model.LaunchEntry) (string, error) {
	entry.Normalize()
	if err := entry.ValidateForShortcut(); err != nil {
		return "", err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return "", fmt.Errorf("生成快捷方式备用配置：%w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeShortcutSpec(value string) (model.LaunchEntry, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64*1024 {
		return model.LaunchEntry{}, fmt.Errorf("快捷方式备用配置无效")
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return model.LaunchEntry{}, fmt.Errorf("解析快捷方式备用配置：%w", err)
	}
	var entry model.LaunchEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return model.LaunchEntry{}, fmt.Errorf("解析快捷方式备用配置：%w", err)
	}
	entry.Normalize()
	if err := entry.ValidateForShortcut(); err != nil {
		return model.LaunchEntry{}, fmt.Errorf("快捷方式备用配置无效：%w", err)
	}
	return entry, nil
}
