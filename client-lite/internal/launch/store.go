package launch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"easy-net/client-lite/internal/model"
)

type store struct {
	path string
	mu   sync.Mutex
}

func newStore(dir string) *store {
	return &store{path: filepath.Join(dir, "launches.json")}
}

func (s *store) Path() string { return s.path }

func (s *store) Load() (*model.LaunchFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &model.LaunchFile{Version: model.CurrentLaunchFileVersion}, nil
		}
		return nil, fmt.Errorf("读取启动入口：%w", err)
	}
	var file model.LaunchFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("解析启动入口：%w", err)
	}
	legacy := file.Version < model.CurrentLaunchFileVersion
	if legacy {
		for _, entry := range file.Entries {
			if entry.AttachExisting || entry.Mode == model.LaunchModeWinDivert {
				file.TakeoverEnabled = true
				break
			}
		}
	}
	valid := make([]model.LaunchEntry, 0, len(file.Entries))
	seen := make(map[string]struct{}, len(file.Entries))
	for _, entry := range file.Entries {
		if legacy {
			entry.AttachExisting = true
		}
		entry.Normalize()
		if entry.Validate() != nil {
			continue
		}
		if _, exists := seen[entry.ID]; exists {
			continue
		}
		seen[entry.ID] = struct{}{}
		valid = append(valid, entry)
	}
	file.Version = model.CurrentLaunchFileVersion
	file.Entries = valid
	return &file, nil
}

func (s *store) Save(file *model.LaunchFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("创建启动入口目录：%w", err)
	}
	copyFile := &model.LaunchFile{
		Version: model.CurrentLaunchFileVersion, TakeoverEnabled: file.TakeoverEnabled,
		Entries: append([]model.LaunchEntry(nil), file.Entries...),
	}
	data, err := json.MarshalIndent(copyFile, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化启动入口：%w", err)
	}
	data = append(data, '\n')
	tmpFile, err := os.CreateTemp(filepath.Dir(s.path), "launches.json.tmp-*")
	if err != nil {
		return fmt.Errorf("写入启动入口：%w", err)
	}
	tmp := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
	}
	if err := tmpFile.Chmod(0600); err != nil {
		cleanup()
		return fmt.Errorf("保护启动入口：%w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("写入启动入口：%w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("同步启动入口：%w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("关闭启动入口：%w", err)
	}
	if err := replaceFile(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存启动入口：%w", err)
	}
	syncFileDirectory(s.path)
	return nil
}
