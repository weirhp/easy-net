package launch

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/service"
)

var ErrNotFound = fmt.Errorf("启动入口不存在")

type StartOptions struct {
	ConfirmRunning bool
}

type AlreadyRunningError struct {
	Entry model.LaunchEntry
}

type ProxyUnavailableError struct {
	ProfileName string
	Address     string
	Cause       error
}

type ApplicationNotRunningError struct {
	Entry model.LaunchEntry
}

func (e *ApplicationNotRunningError) Error() string {
	return fmt.Sprintf("没有检测到正在运行的 %s，已中止接管", e.Entry.Name)
}

func (e *ProxyUnavailableError) Error() string {
	return fmt.Sprintf("代理“%s”（%s）不可用，已中止启动：%v", e.ProfileName, e.Address, e.Cause)
}

func (e *ProxyUnavailableError) Unwrap() error { return e.Cause }

func (e *AlreadyRunningError) Error() string {
	return fmt.Sprintf("%s 已经在运行", e.Entry.Name)
}

type Service struct {
	mu      sync.Mutex
	startMu sync.Mutex
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
	ExternalProxy   bool   `json:"externalProxy"`
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
		} else if entry.Proxy != "" {
			view.ProfileName = "手动 SOCKS5"
			view.ListenAddress = entry.Proxy
			view.ExternalProxy = true
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

func (s *Service) Processes() ([]ProcessInfo, error) {
	return s.runner.Processes()
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
	return s.StartWithOptions(id, StartOptions{})
}

func (s *Service) StartWithOptions(id string, options StartOptions) (View, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	entry, ok := s.Get(id)
	if !ok {
		return View{}, ErrNotFound
	}
	if err := entry.ValidateForStart(); err != nil {
		return View{}, err
	}
	running, err := s.runner.IsRunning(entry)
	if err != nil {
		return View{}, err
	}
	if entry.AttachExisting {
		if !running {
			return View{}, &ApplicationNotRunningError{Entry: entry}
		}
	} else if !options.ConfirmRunning {
		if running {
			return View{}, &AlreadyRunningError{Entry: entry}
		}
	}
	proxyAddress := entry.Proxy
	profileName := "手动 SOCKS5"
	profileRunning := false
	if entry.ProfileID != "" {
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
		proxyAddress = profile.ListenAddress()
		profileName = profile.Name
		profileRunning = true
	}
	if err := s.runner.CheckProxy(proxyAddress); err != nil {
		return View{}, &ProxyUnavailableError{ProfileName: profileName, Address: proxyAddress, Cause: err}
	}
	args, err := HookArgs(entry, proxyAddress)
	if err != nil {
		return View{}, err
	}
	if usesSharedWinDivert(entry) {
		profilePath, profileErr := s.writeSharedWinDivertProfile()
		if profileErr != nil {
			return View{}, profileErr
		}
		args = insertHookOptions(args,
			"--windivert-shared-profile", profilePath,
			"--windivert-shared-root", strconv.Itoa(os.Getpid()))
	}
	if err := s.runner.Start(args); err != nil {
		return View{}, err
	}
	view := View{
		LaunchEntry:    entry,
		ModeLabel:      entry.Mode.Label(),
		ProfileName:    profileName,
		ListenAddress:  proxyAddress,
		ProfileRunning: profileRunning,
		ExternalProxy:  entry.Proxy != "",
	}
	return view, nil
}

func insertHookOptions(args []string, values ...string) []string {
	separator := len(args)
	for index, argument := range args {
		if argument == "--" {
			separator = index
			break
		}
	}
	result := make([]string, 0, len(args)+len(values))
	result = append(result, args[:separator]...)
	result = append(result, values...)
	result = append(result, args[separator:]...)
	return result
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
