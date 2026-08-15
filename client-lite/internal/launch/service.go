package launch

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/service"
)

var ErrNotFound = fmt.Errorf("启动入口不存在")

type Service struct {
	mu      sync.Mutex
	store   *store
	file    *model.LaunchFile
	proxies *service.Service
	runner  Runner
}

type View struct {
	model.LaunchEntry
	ModeLabel       string `json:"modeLabel"`
	ProfileName     string `json:"profileName,omitempty"`
	ListenAddress   string `json:"listenAddress,omitempty"`
	ProfileRunning  bool   `json:"profileRunning"`
	ProfileStarting bool   `json:"profileStarting"`
}

func New(dir string, proxies *service.Service, runner Runner) (*Service, error) {
	if runner == nil {
		runner = DefaultRunner()
	}
	s := &Service{store: newStore(dir), proxies: proxies, runner: runner}
	file, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(s.store.Path()); os.IsNotExist(err) {
		migrated, migrateErr := migrateFromHistory(proxies, defaultHistoryPath())
		if migrateErr != nil {
			return nil, migrateErr
		}
		if len(migrated) > 0 {
			file.Entries = migrated
			if err := s.store.Save(file); err != nil {
				return nil, err
			}
		}
	}
	s.file = file
	return s, nil
}

func Supported() bool { return runtime.GOOS == "windows" }

func (s *Service) List() []model.LaunchEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.LaunchEntry, len(s.file.Entries))
	copy(out, s.file.Entries)
	return out
}

func (s *Service) Views() []View {
	states := map[string]service.ProfileState{}
	if s.proxies != nil {
		for _, state := range s.proxies.States() {
			states[state.Profile.ID] = state
		}
	}
	entries := s.List()
	views := make([]View, 0, len(entries))
	for _, entry := range entries {
		view := View{LaunchEntry: entry, ModeLabel: entry.Mode.Label()}
		if state, ok := states[entry.ProfileID]; ok {
			view.ProfileName = state.Profile.Name
			view.ListenAddress = state.Profile.ListenAddress()
			view.ProfileRunning = state.Running
			view.ProfileStarting = state.Starting
		}
		views = append(views, view)
	}
	return views
}

func (s *Service) Get(id string) (model.LaunchEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.indexLocked(id)
	if index < 0 {
		return model.LaunchEntry{}, false
	}
	return s.file.Entries[index].Clone(), true
}

func (s *Service) Upsert(entry model.LaunchEntry) (model.LaunchEntry, error) {
	entry.Normalize()
	if entry.ID == "" {
		entry.ID = newID()
	}
	if err := entry.Validate(); err != nil {
		return model.LaunchEntry{}, err
	}
	if entry.ProfileID != "" && s.proxies != nil {
		if _, ok := s.proxies.Profile(entry.ProfileID); !ok {
			return model.LaunchEntry{}, fmt.Errorf("代理配置不存在")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.indexLocked(entry.ID)
	if index < 0 && len(s.file.Entries) >= model.MaxLaunchEntries {
		return model.LaunchEntry{}, fmt.Errorf("启动入口最多 %d 个", model.MaxLaunchEntries)
	}
	if index >= 0 {
		s.file.Entries[index] = entry
	} else {
		s.file.Entries = append(s.file.Entries, entry)
	}
	if err := s.store.Save(s.file); err != nil {
		return model.LaunchEntry{}, err
	}
	return entry, nil
}

func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.indexLocked(id)
	if index < 0 {
		return fmt.Errorf("启动入口不存在")
	}
	s.file.Entries = append(s.file.Entries[:index], s.file.Entries[index+1:]...)
	return s.store.Save(s.file)
}

func (s *Service) Start(id string) (View, error) {
	entry, ok := s.Get(id)
	if !ok {
		return View{}, ErrNotFound
	}
	if err := entry.ValidateForStart(); err != nil {
		return View{}, err
	}
	if s.proxies == nil {
		return View{}, fmt.Errorf("代理服务不可用")
	}
	if err := s.proxies.Start(entry.ProfileID); err != nil {
		return View{}, fmt.Errorf("启动本地代理失败：%w", err)
	}
	profile, ok := s.proxies.Profile(entry.ProfileID)
	if !ok {
		return View{}, fmt.Errorf("代理配置不存在")
	}
	args, err := HookArgs(entry, profile.ListenAddress())
	if err != nil {
		return View{}, err
	}
	if err := s.runner.Start(args); err != nil {
		return View{}, err
	}
	view := View{
		LaunchEntry:    entry,
		ModeLabel:      entry.Mode.Label(),
		ProfileName:    profile.Name,
		ListenAddress:  profile.ListenAddress(),
		ProfileRunning: true,
	}
	return view, nil
}

func (s *Service) CreateShortcut(id string) (string, error) {
	entry, ok := s.Get(id)
	if !ok {
		return "", fmt.Errorf("启动入口不存在")
	}
	if err := entry.Validate(); err != nil {
		return "", err
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位 Easy-Net Lite：%w", err)
	}
	return s.runner.CreateShortcut(ShortcutOptions{
		Name:             entry.Name,
		Target:           self,
		Arguments:        "--launch-entry " + entry.ID,
		WorkingDirectory: filepath.Dir(self),
		Description:      "通过 Easy-Net Lite 代理启动 " + entry.Name,
		IconPath:         entry.Path,
		UseChatGPTIcon:   entry.Mode == model.LaunchModeChatGPT,
	})
}

func (s *Service) indexLocked(id string) int {
	id = strings.TrimSpace(id)
	for index, entry := range s.file.Entries {
		if entry.ID == id {
			return index
		}
	}
	return -1
}
