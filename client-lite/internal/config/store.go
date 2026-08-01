package config

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"easy-net/client-lite/internal/model"
)

type Store struct {
	path     string
	mu       sync.RWMutex
	warnings []string
}

func NewStore() (*Store, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("获取配置目录：%w", err)
	}
	return &Store{path: filepath.Join(base, "Easy-Net Lite", "config.json")}, nil
}

func NewStoreAt(path string) *Store { return &Store{path: path} }

func (s *Store) Path() string { return s.path }

func (s *Store) Dir() string { return filepath.Dir(s.path) }

func (s *Store) Warnings() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.warnings...)
}

func (s *Store) setWarnings(warnings []string) {
	s.mu.Lock()
	s.warnings = append([]string(nil), warnings...)
	s.mu.Unlock()
	for _, warning := range warnings {
		log.Printf("[Easy-Net Lite] 配置恢复：%s", warning)
	}
}

func (s *Store) Load() (*model.Config, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.setWarnings(nil)
			return emptyConfig(), nil
		}
		return nil, fmt.Errorf("读取配置：%w", err)
	}

	cfg, warnings, parseErr := parseConfig(data)
	if parseErr == nil {
		if len(warnings) > 0 {
			recoveryPath, recoveryErr := s.preserveRecoveryCopy(data)
			if recoveryErr != nil {
				warnings = append(warnings, "保存原始配置副本失败："+recoveryErr.Error())
			} else {
				warnings = append(warnings, "原始配置已保存在 "+recoveryPath)
			}
		}
		s.setWarnings(warnings)
		return cfg, nil
	}

	backupData, backupErr := os.ReadFile(s.backupPath())
	if backupErr == nil {
		backupCfg, backupWarnings, backupParseErr := parseConfig(backupData)
		if backupParseErr == nil {
			warning := "主配置损坏，已从备份恢复：" + parseErr.Error()
			warnings = append([]string{warning}, backupWarnings...)
			if recoveryPath, recoveryErr := s.preserveRecoveryCopy(data); recoveryErr == nil {
				warnings = append(warnings, "损坏配置已保存在 "+recoveryPath)
			}
			if restoreErr := writeAtomic(s.path, backupData); restoreErr != nil {
				warnings = append(warnings, "恢复主配置文件失败："+restoreErr.Error())
			}
			s.setWarnings(warnings)
			return backupCfg, nil
		}
	}

	warning := "配置无法解析，已使用空配置启动：" + parseErr.Error()
	if recoveryPath, recoveryErr := s.preserveRecoveryCopy(data); recoveryErr == nil {
		warning += "；原文件保存在 " + recoveryPath
	}
	s.setWarnings([]string{warning})
	return emptyConfig(), nil
}

func parseConfig(data []byte) (*model.Config, []string, error) {
	var cfg model.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, nil, fmt.Errorf("解析配置：%w", err)
	}
	legacy := cfg.Version < model.CurrentConfigVersion
	valid := make([]model.Profile, 0, len(cfg.Profiles))
	warnings := make([]string, 0)
	seenIDs := make(map[string]struct{})
	seenPorts := make(map[string]struct{})
	for i := range cfg.Profiles {
		profile := cfg.Profiles[i].Clone()
		profile.Normalize()
		if legacy && profile.WebSocket != nil {
			profile.WebSocket.LegacyQueryAuth = true
			lowerURL := strings.ToLower(profile.WebSocket.URL)
			if strings.HasPrefix(lowerURL, "ws://") || strings.HasPrefix(lowerURL, "http://") {
				profile.WebSocket.AllowInsecure = true
			}
		}
		if err := profile.Validate(); err != nil {
			warnings = append(warnings, fmt.Sprintf("已跳过无效配置 %q：%v", profile.Name, err))
			continue
		}
		if _, exists := seenIDs[profile.ID]; exists {
			warnings = append(warnings, fmt.Sprintf("已跳过 ID 重复的配置 %q", profile.Name))
			continue
		}
		listenAddress := profile.ListenAddress()
		if _, exists := seenPorts[listenAddress]; exists {
			warnings = append(warnings, fmt.Sprintf("已跳过本地端口重复的配置 %q", profile.Name))
			continue
		}
		seenIDs[profile.ID] = struct{}{}
		seenPorts[listenAddress] = struct{}{}
		valid = append(valid, profile)
	}
	cfg.Version = model.CurrentConfigVersion
	cfg.Profiles = valid
	return &cfg, warnings, nil
}

func emptyConfig() *model.Config {
	return &model.Config{Version: model.CurrentConfigVersion, Profiles: []model.Profile{}}
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
	if err := writeExclusive(path, data, 0600); err != nil {
		return "", fmt.Errorf("保存私钥文件：%w", err)
	}
	return path, nil
}

func (s *Store) DeleteManagedPrivateKey(path string) error {
	if path == "" {
		return nil
	}
	target, managed, err := s.managedPrivateKeyPath(path)
	if err != nil || !managed {
		return err
	}
	err = os.Remove(target)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Store) ReadManagedPrivateKey(path string) ([]byte, error) {
	target, managed, err := s.managedPrivateKeyPath(path)
	if err != nil {
		return nil, err
	}
	if !managed {
		return nil, fmt.Errorf("SSH 私钥不在应用托管目录中")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("读取 SSH 私钥：%w", err)
	}
	if len(data) == 0 || len(data) > 64*1024 {
		return nil, fmt.Errorf("SSH 私钥内容无效")
	}
	return data, nil
}

func (s *Store) managedPrivateKeyPath(path string) (string, bool, error) {
	keysDir, err := filepath.Abs(filepath.Join(s.Dir(), "keys"))
	if err != nil {
		return "", false, err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return "", false, err
	}
	rel, err := filepath.Rel(keysDir, target)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return target, false, err
	}
	return target, true, nil
}

func (s *Store) Save(cfg *model.Config) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("创建配置目录：%w", err)
	}
	copyConfig := cloneConfig(cfg)
	copyConfig.Version = model.CurrentConfigVersion
	if err := validateForSave(copyConfig); err != nil {
		return err
	}
	data, err := json.MarshalIndent(copyConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置：%w", err)
	}
	data = append(data, '\n')
	if current, readErr := os.ReadFile(s.path); readErr == nil {
		if err := writeAtomic(s.backupPath(), current); err != nil {
			return fmt.Errorf("保存配置备份：%w", err)
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("读取现有配置：%w", readErr)
	}
	if err := writeAtomic(s.path, data); err != nil {
		return fmt.Errorf("保存配置：%w", err)
	}
	s.setWarnings(nil)
	return nil
}

func validateForSave(cfg *model.Config) error {
	seenIDs := make(map[string]struct{}, len(cfg.Profiles))
	seenAddresses := make(map[string]struct{}, len(cfg.Profiles))
	for i := range cfg.Profiles {
		cfg.Profiles[i].Normalize()
		profile := cfg.Profiles[i]
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("配置 %q 无效：%w", profile.Name, err)
		}
		if _, exists := seenIDs[profile.ID]; exists {
			return fmt.Errorf("配置 ID %q 重复", profile.ID)
		}
		address := profile.ListenAddress()
		if _, exists := seenAddresses[address]; exists {
			return fmt.Errorf("本地监听地址 %s 重复", address)
		}
		seenIDs[profile.ID] = struct{}{}
		seenAddresses[address] = struct{}{}
	}
	return nil
}

func cloneConfig(cfg *model.Config) *model.Config {
	result := &model.Config{Version: cfg.Version, Profiles: make([]model.Profile, len(cfg.Profiles))}
	for i := range cfg.Profiles {
		result.Profiles[i] = cfg.Profiles[i].Clone()
	}
	return result
}

func (s *Store) backupPath() string { return s.path + ".bak" }

func (s *Store) preserveRecoveryCopy(data []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return "", err
	}
	path := fmt.Sprintf("%s.recovery-%s", s.path, time.Now().Format("20060102-150405.000000000"))
	if err := writeExclusive(path, data, 0600); err != nil {
		return "", err
	}
	return path, nil
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = file.Chmod(mode)
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	if err := hardenFile(path); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}
	if runtime.GOOS != "windows" {
		if err := file.Chmod(0600); err != nil {
			cleanup()
			return err
		}
	}
	if _, err := file.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := hardenFile(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if runtime.GOOS != "windows" {
		if dirFile, openErr := os.Open(dir); openErr == nil {
			_ = dirFile.Sync()
			_ = dirFile.Close()
		}
	}
	return nil
}
