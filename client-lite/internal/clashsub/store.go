package clashsub

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
	return &store{path: filepath.Join(dir, "subscriptions.json")}
}

func (s *store) Load() (*model.SubscriptionFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &model.SubscriptionFile{Version: model.CurrentSubscriptionFileVersion}, nil
		}
		return nil, fmt.Errorf("读取节点订阅：%w", err)
	}
	var file model.SubscriptionFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("解析节点订阅：%w", err)
	}
	legacyBypass := file.Version < 3
	valid := make([]model.Subscription, 0, len(file.Subscriptions))
	seen := map[string]struct{}{}
	for _, item := range file.Subscriptions {
		if legacyBypass {
			item.BypassPrivate = true
			item.BypassChina = true
		}
		item.Normalize()
		for index := range item.Nodes {
			raw := normalizeMap(item.Nodes[index].Raw)
			applyNodeRuntimeDefaults(raw)
			item.Nodes[index].Raw = raw
		}
		if item.Validate() != nil {
			continue
		}
		if _, exists := seen[item.ID]; exists {
			continue
		}
		seen[item.ID] = struct{}{}
		valid = append(valid, item)
		if len(valid) >= model.MaxClashSubscriptions {
			break
		}
	}
	file.Version = model.CurrentSubscriptionFileVersion
	file.Subscriptions = valid
	return &file, nil
}

func (s *store) Save(file *model.SubscriptionFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("创建订阅目录：%w", err)
	}
	copyFile := &model.SubscriptionFile{
		Version:       model.CurrentSubscriptionFileVersion,
		Subscriptions: append([]model.Subscription(nil), file.Subscriptions...),
	}
	data, err := json.MarshalIndent(copyFile, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化节点订阅：%w", err)
	}
	data = append(data, '\n')
	tmpFile, err := os.CreateTemp(filepath.Dir(s.path), "subscriptions.json.tmp-*")
	if err != nil {
		return fmt.Errorf("写入节点订阅：%w", err)
	}
	tmp := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
	}
	if err := tmpFile.Chmod(0600); err != nil {
		cleanup()
		return fmt.Errorf("保护节点订阅：%w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("写入节点订阅：%w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("同步节点订阅：%w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("关闭节点订阅：%w", err)
	}
	if err := replaceRuntimeFile(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存节点订阅：%w", err)
	}
	syncRuntimeFileDirectory(s.path)
	return nil
}
