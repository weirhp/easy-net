package config

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"easy-net/client-lite/internal/model"
)

type Store struct {
	path string
}

func NewStore() (*Store, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("获取配置目录：%w", err)
	}
	return &Store{path: filepath.Join(base, "Easy-Net Lite", "config.json")}, nil
}

func NewStoreAt(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Path() string { return s.path }

func (s *Store) Dir() string { return filepath.Dir(s.path) }

func (s *Store) Load() (*model.Config, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &model.Config{Profiles: []model.Profile{}}, nil
		}
		return nil, fmt.Errorf("读取配置：%w", err)
	}
	var cfg model.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置：%w", err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = []model.Profile{}
	}
	for i := range cfg.Profiles {
		cfg.Profiles[i].Normalize()
		if err := cfg.Profiles[i].Validate(); err != nil {
			return nil, fmt.Errorf("配置 %q 无效：%w", cfg.Profiles[i].Name, err)
		}
	}
	return &cfg, nil
}

func (s *Store) SavePrivateKey(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("私钥文件不能为空")
	}
	if len(data) > 64*1024 {
		return "", fmt.Errorf("私钥文件不能超过 64 KiB")
	}
	keysDir := filepath.Join(s.Dir(), "keys")
	if err := os.MkdirAll(keysDir, 0700); err != nil {
		return "", fmt.Errorf("创建私钥目录：%w", err)
	}
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("生成私钥文件名：%w", err)
	}
	path := filepath.Join(keysDir, fmt.Sprintf("key-%x.pem", random))
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", fmt.Errorf("保存私钥文件：%w", err)
	}
	return path, nil
}

func (s *Store) DeleteManagedPrivateKey(path string) error {
	if path == "" {
		return nil
	}
	keysDir, err := filepath.Abs(filepath.Join(s.Dir(), "keys"))
	if err != nil {
		return err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(keysDir, target)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	err = os.Remove(target)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Store) Save(cfg *model.Config) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("创建配置目录：%w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置：%w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("写入临时配置：%w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存配置：%w", err)
	}
	return nil
}
