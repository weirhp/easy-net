package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/service"
)

var ErrNotFound = fmt.Errorf("该快捷方式对应的应用配置已不存在，请在“应用代理管理”中重新添加应用并创建快捷方式")

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

type WinDivertStartError struct {
	Cause error
}

func (e *WinDivertStartError) Error() string {
	return fmt.Sprintf("WinDivert 接管未启动：%v", e.Cause)
}

func (e *WinDivertStartError) Unwrap() error { return e.Cause }

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
	mu       sync.Mutex
	startMu  sync.Mutex
	pickerMu sync.Mutex
	store    *store
	file     *model.LaunchFile
	proxies  *service.Service
	runner   Runner
	statusMu sync.Mutex
	status   TakeoverStatus
}

type TakeoverStatus struct {
	Enabled      bool   `json:"enabled"`
	State        string `json:"state"`
	Message      string `json:"message"`
	RestartCount int    `json:"restartCount"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
}

type sharedSupervisorStatus struct {
	State        string `json:"State"`
	Message      string `json:"Message"`
	RestartCount int    `json:"RestartCount"`
	UpdatedAtMS  int64  `json:"UpdatedAtUnixMs"`
}

type View struct {
	model.LaunchEntry
	ModeLabel       string `json:"modeLabel"`
	ProfileName     string `json:"profileName,omitempty"`
	ListenAddress   string `json:"listenAddress,omitempty"`
	ProfileRunning  bool   `json:"profileRunning"`
	ProfileStarting bool   `json:"profileStarting"`
	ExternalProxy   bool   `json:"externalProxy"`
	UsesDefault     bool   `json:"usesDefault"`
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
			for index := range migrated {
				migrated[index].AttachExisting = true
				migrated[index].Normalize()
			}
			file.Entries = migrated
			if err := s.store.Save(file); err != nil {
				return nil, err
			}
		}
	}
	s.file = file
	s.status = TakeoverStatus{Enabled: file.TakeoverEnabled, State: "stopped", Message: "应用网络接管未开启"}
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

func (s *Service) TakeoverEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.TakeoverEnabled
}

func (s *Service) SetTakeoverEnabled(enabled bool) error {
	s.mu.Lock()
	updated := &model.LaunchFile{
		Version: s.file.Version, TakeoverEnabled: enabled,
		Entries: append([]model.LaunchEntry(nil), s.file.Entries...),
	}
	if err := s.store.Save(updated); err != nil {
		s.mu.Unlock()
		return err
	}
	s.file = updated
	s.mu.Unlock()
	s.setTakeoverStatus(TakeoverStatus{Enabled: enabled, State: map[bool]string{true: "starting", false: "stopped"}[enabled], Message: map[bool]string{true: "正在启动应用网络接管", false: "应用网络接管已关闭"}[enabled]})
	return s.ApplySharedRules()
}

func (s *Service) TakeoverStatus() TakeoverStatus {
	enabled := s.TakeoverEnabled()
	status := s.readSharedSupervisorStatus()
	s.statusMu.Lock()
	local := s.status
	s.statusMu.Unlock()
	if !enabled {
		return TakeoverStatus{Enabled: false, State: "stopped", Message: "应用网络接管已关闭"}
	}
	if status != nil {
		age := time.Since(time.UnixMilli(status.UpdatedAtMS))
		if age >= 0 && age <= 12*time.Second {
			message := status.Message
			switch status.State {
			case "healthy":
				message = "接管服务运行正常；列表内应用的新连接会自动走代理"
			case "starting":
				message = "正在启动应用网络接管"
			case "restarting":
				message = "接管引擎意外退出或规则已更新，正在自动重启"
			case "error":
				message = "接管引擎启动失败，后台仍在自动重试：" + status.Message + "；日志：" + filepath.Join(filepath.Dir(s.store.Path()), "shared-windivert.log")
			case "stopped":
				message = "应用网络接管已停止"
			}
			return TakeoverStatus{
				Enabled: true, State: status.State, Message: message,
				RestartCount: status.RestartCount, UpdatedAt: time.UnixMilli(status.UpdatedAtMS).Format(time.RFC3339),
			}
		}
	}
	local.Enabled = true
	if local.State == "starting" && local.UpdatedAt != "" {
		if started, err := time.Parse(time.RFC3339, local.UpdatedAt); err == nil && time.Since(started) > 15*time.Second {
			local.State = "error"
			local.Message = "接管服务启动超时，Lite 将自动尝试恢复"
		}
	}
	if local.State == "healthy" || local.State == "restarting" {
		local.State = "error"
		local.Message = "接管服务状态已失联，Lite 将自动尝试恢复"
	}
	return local
}

func (s *Service) StartTakeoverMonitor(ctx context.Context, report func(string, ...any)) {
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		failures := 0
		lastAttempt := time.Time{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !s.TakeoverEnabled() {
					failures = 0
					continue
				}
				status := s.TakeoverStatus()
				if status.State == "healthy" || status.State == "starting" || status.State == "restarting" || status.State == "idle" {
					continue
				}
				if status.State == "error" && status.UpdatedAt != "" {
					if updated, err := time.Parse(time.RFC3339, status.UpdatedAt); err == nil && time.Since(updated) < 12*time.Second {
						// The elevated C++ supervisor is alive and performs its own
						// bounded restart loop. Avoid launching a competing supervisor.
						continue
					}
				}
				delay := 10 * time.Second
				if failures > 0 {
					delay = time.Duration(1<<min(failures, 5)) * 10 * time.Second
				}
				if time.Since(lastAttempt) < delay {
					continue
				}
				lastAttempt = time.Now()
				if err := s.ApplySharedRules(); err != nil {
					failures++
					s.setTakeoverStatus(TakeoverStatus{Enabled: true, State: "error", Message: err.Error(), RestartCount: failures, UpdatedAt: time.Now().Format(time.RFC3339)})
					if report != nil {
						report("[Easy-Net Lite] 自动恢复应用网络接管失败：%v", err)
					}
				} else {
					failures = 0
				}
			}
		}
	}()
}

func (s *Service) setTakeoverStatus(status TakeoverStatus) {
	s.statusMu.Lock()
	s.status = status
	s.statusMu.Unlock()
}

func (s *Service) sharedStatusPath() string {
	return filepath.Join(filepath.Dir(s.store.Path()), "shared-windivert-status.json")
}

func (s *Service) readSharedSupervisorStatus() *sharedSupervisorStatus {
	data, err := os.ReadFile(s.sharedStatusPath())
	if err != nil {
		return nil
	}
	var status sharedSupervisorStatus
	if json.Unmarshal(data, &status) != nil || status.State == "" || status.UpdatedAtMS <= 0 {
		return nil
	}
	return &status
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
		profileID := entry.ProfileID
		if profileID == "" && entry.Proxy == "" && s.proxies != nil {
			if profile, ok := s.proxies.DefaultProfile(); ok {
				profileID = profile.ID
				view.UsesDefault = true
			}
		}
		if state, ok := states[profileID]; ok {
			view.ProfileName = state.Profile.Name
			view.ListenAddress = state.Profile.ListenAddress()
			view.ProfileRunning = state.Running || state.Profile.Type == model.ProxyTypeExternal
			view.ProfileStarting = state.Starting
			view.ExternalProxy = state.Profile.Type == model.ProxyTypeExternal
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

// RestoreShortcutEntry restores the snapshot embedded in a desktop shortcut
// only when its original entry no longer exists. Existing entries always win,
// so later edits to proxy and routing settings remain effective.
func (s *Service) RestoreShortcutEntry(id, spec string) (model.LaunchEntry, bool, error) {
	if entry, ok := s.Get(id); ok {
		return entry, false, nil
	}
	if strings.TrimSpace(spec) == "" {
		return model.LaunchEntry{}, false, ErrNotFound
	}
	entry, err := decodeShortcutSpec(spec)
	if err != nil {
		return model.LaunchEntry{}, false, err
	}
	if entry.ID != id {
		return model.LaunchEntry{}, false, fmt.Errorf("快捷方式备用配置与启动入口不匹配")
	}
	saved, err := s.Upsert(entry)
	if err != nil {
		return model.LaunchEntry{}, false, fmt.Errorf("恢复快捷方式应用配置：%w", err)
	}
	return saved, true, nil
}

func (s *Service) Processes() ([]ProcessInfo, error) {
	return s.runner.Processes()
}

func (s *Service) PickApplicationFiles(kind string) ([]PickedApplication, error) {
	if !s.pickerMu.TryLock() {
		return nil, fmt.Errorf("文件选择窗口已经打开，请先完成或取消当前选择")
	}
	defer s.pickerMu.Unlock()
	return pickApplicationFiles(kind)
}

func (s *Service) ProxyUsageCount(profileID string) int {
	defaultID := ""
	if s.proxies != nil {
		if profile, ok := s.proxies.DefaultProfile(); ok {
			defaultID = profile.ID
		}
	}
	count := 0
	for _, entry := range s.List() {
		if entry.ProfileID == profileID || entry.ProfileID == "" && entry.Proxy == "" && defaultID == profileID {
			count++
		}
	}
	return count
}

func (s *Service) DefaultProxyUsageCount() int {
	count := 0
	for _, entry := range s.List() {
		if entry.ProfileID == "" && entry.Proxy == "" {
			count++
		}
	}
	return count
}

func (s *Service) Upsert(entry model.LaunchEntry) (model.LaunchEntry, error) {
	saved, err := s.UpsertMany([]model.LaunchEntry{entry})
	if err != nil {
		return model.LaunchEntry{}, err
	}
	return saved[0], nil
}

func (s *Service) UpsertMany(entries []model.LaunchEntry) ([]model.LaunchEntry, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("请至少选择一个应用")
	}
	if len(entries) > model.MaxLaunchEntries {
		return nil, fmt.Errorf("一次最多添加 %d 个应用", model.MaxLaunchEntries)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := &model.LaunchFile{Version: s.file.Version, TakeoverEnabled: s.file.TakeoverEnabled, Entries: append([]model.LaunchEntry(nil), s.file.Entries...)}
	saved := make([]model.LaunchEntry, 0, len(entries))
	for _, entry := range entries {
		entry.Normalize()
		if entry.ID == "" {
			for _, existing := range updated.Entries {
				if sameManagedApplication(existing, entry) {
					entry.ID = existing.ID
					break
				}
			}
			if entry.ID == "" {
				entry.ID = newID()
			}
		}
		if err := entry.Validate(); err != nil {
			return nil, err
		}
		if entry.ProfileID != "" && s.proxies != nil {
			if _, ok := s.proxies.Profile(entry.ProfileID); !ok {
				return nil, fmt.Errorf("代理配置不存在")
			}
		}
		index := launchEntryIndex(updated.Entries, entry.ID)
		if index < 0 && len(updated.Entries) >= model.MaxLaunchEntries {
			return nil, fmt.Errorf("启动入口最多 %d 个", model.MaxLaunchEntries)
		}
		if index >= 0 {
			updated.Entries[index] = entry
		} else {
			updated.Entries = append(updated.Entries, entry)
		}
		saved = append(saved, entry)
	}
	if err := s.store.Save(updated); err != nil {
		return nil, err
	}
	s.file = updated
	return saved, nil
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
	if err := entry.ValidateForShortcut(); err != nil {
		return View{}, err
	}
	launchEntry := entry
	launchEntry.AttachExisting = false
	launchEntry.WeChatExisting = false
	if launchEntry.Mode == model.LaunchModeWinDivert {
		launchEntry.Mode = model.LaunchModeHook
	}
	running, err := s.runner.IsRunning(launchEntry)
	if err != nil {
		return View{}, err
	}
	if !options.ConfirmRunning {
		if running {
			return View{}, &AlreadyRunningError{Entry: entry}
		}
	}
	proxyAddress := entry.Proxy
	profileName := "手动 SOCKS5"
	profileRunning := false
	profileID := entry.ProfileID
	usesDefault := false
	if profileID == "" && proxyAddress == "" && s.proxies != nil {
		if profile, found := s.proxies.DefaultProfile(); found {
			profileID = profile.ID
			usesDefault = true
		}
	}
	if profileID != "" {
		if s.proxies == nil {
			return View{}, fmt.Errorf("代理服务不可用")
		}
		profile, ok := s.proxies.Profile(profileID)
		if !ok {
			return View{}, fmt.Errorf("代理配置不存在")
		}
		if profile.Type != model.ProxyTypeExternal {
			if err := s.proxies.Start(profileID); err != nil {
				return View{}, fmt.Errorf("启动本地代理失败：%w", err)
			}
			profileRunning = true
		}
		proxyAddress = profile.ListenAddress()
		profileName = profile.Name
	}
	if proxyAddress == "" {
		return View{}, fmt.Errorf("尚未设置默认代理；请先在网络代理列表中选择一个默认代理")
	}
	if err := s.runner.CheckProxy(proxyAddress); err != nil {
		return View{}, &ProxyUnavailableError{ProfileName: profileName, Address: proxyAddress, Cause: err}
	}
	if s.TakeoverEnabled() {
		if err := s.applySharedRulesLocked(); err != nil {
			return View{}, err
		}
	}
	launchEntry.ProfileID = ""
	launchEntry.Proxy = proxyAddress
	args, err := HookArgs(launchEntry, proxyAddress)
	if err != nil {
		return View{}, err
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
		ExternalProxy:  entry.Proxy != "" || profileID != "" && !profileRunning,
		UsesDefault:    usesDefault,
	}
	return view, nil
}

// ApplySharedRules updates the one Lite-owned WinDivert engine used by every
// takeover entry. It intentionally does not require the target process to be
// running: the process-name rules also match applications started later.
func (s *Service) ApplySharedRules() error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	err := s.applySharedRulesLocked()
	if err != nil && s.TakeoverEnabled() {
		s.setTakeoverStatus(TakeoverStatus{Enabled: true, State: "error", Message: err.Error(), UpdatedAt: time.Now().Format(time.RFC3339)})
	}
	return err
}

func (s *Service) applySharedRulesLocked() error {
	if !s.TakeoverEnabled() {
		if _, err := s.writeSharedWinDivertProfile(); err != nil {
			return err
		}
		s.setTakeoverStatus(TakeoverStatus{Enabled: false, State: "stopped", Message: "应用网络接管已关闭", UpdatedAt: time.Now().Format(time.RFC3339)})
		return nil
	}
	entries := s.List()
	rules := append([]model.LaunchEntry(nil), entries...)
	if len(rules) == 0 {
		_, err := s.writeSharedWinDivertProfile()
		if err == nil {
			s.setTakeoverStatus(TakeoverStatus{Enabled: true, State: "idle", Message: "已开启；添加应用后将自动接管", UpdatedAt: time.Now().Format(time.RFC3339)})
		}
		return err
	}
	s.setTakeoverStatus(TakeoverStatus{Enabled: true, State: "starting", Message: "正在启动应用网络接管", UpdatedAt: time.Now().Format(time.RFC3339)})
	firstProxy := ""
	checked := make(map[string]struct{})
	for _, entry := range rules {
		address, name, err := s.prepareEntryProxy(entry)
		if err != nil {
			return err
		}
		if _, exists := checked[address]; !exists {
			if err := s.runner.CheckProxy(address); err != nil {
				return &ProxyUnavailableError{ProfileName: name, Address: address, Cause: err}
			}
			checked[address] = struct{}{}
		}
		if firstProxy == "" {
			firstProxy = address
		}
	}
	profilePath, err := s.writeSharedWinDivertProfile()
	if err != nil {
		return err
	}
	args := []string{
		"--proxy", firstProxy, "--detach", "--gui-worker", "--windivert",
		"--tun-udp", "auto", "--windivert-existing",
		"--windivert-processes", "easy-net-shared-rule.exe",
		"--windivert-shared-profile", profilePath,
		"--windivert-shared-root", strconv.Itoa(os.Getpid()),
	}
	if err := s.runner.Start(args); err != nil {
		var hookError *HookStartError
		if errors.As(err, &hookError) && hookError.ExitCode == 5 {
			return &WinDivertStartError{Cause: err}
		}
		return err
	}
	s.setTakeoverStatus(TakeoverStatus{Enabled: true, State: "healthy", Message: "接管服务运行中；列表内应用的新连接将自动走代理", UpdatedAt: time.Now().Format(time.RFC3339)})
	return nil
}

func (s *Service) prepareEntryProxy(entry model.LaunchEntry) (string, string, error) {
	if entry.Proxy != "" {
		return entry.Proxy, "手动 SOCKS5", nil
	}
	if s.proxies == nil {
		return "", "", fmt.Errorf("代理服务不可用")
	}
	var profile model.Profile
	var ok bool
	if entry.ProfileID == "" {
		profile, ok = s.proxies.DefaultProfile()
		if !ok {
			return "", "", fmt.Errorf("尚未设置默认代理；请先在网络代理列表中选择一个默认代理")
		}
	} else {
		profile, ok = s.proxies.Profile(entry.ProfileID)
		if !ok {
			return "", "", fmt.Errorf("代理配置不存在")
		}
	}
	if profile.Type != model.ProxyTypeExternal {
		if err := s.proxies.Start(profile.ID); err != nil {
			return "", "", fmt.Errorf("启动本地代理失败：%w", err)
		}
	}
	return profile.ListenAddress(), profile.Name, nil
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
	if err := entry.ValidateForShortcut(); err != nil {
		return "", err
	}
	spec, err := encodeShortcutSpec(entry)
	if err != nil {
		return "", err
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位 Easy-Net Lite：%w", err)
	}
	return s.runner.CreateShortcut(ShortcutOptions{
		Name:             entry.Name,
		Target:           self,
		Arguments:        "--launch-entry " + entry.ID + " --launch-spec " + spec,
		WorkingDirectory: filepath.Dir(self),
		Description:      "通过 Easy-Net Lite 代理启动 " + entry.Name,
		IconPath:         entry.Path,
		UseChatGPTIcon:   entry.Mode == model.LaunchModeChatGPT,
	})
}

func sameManagedApplication(left, right model.LaunchEntry) bool {
	if left.Path != "" && right.Path != "" {
		return strings.EqualFold(filepath.Clean(left.Path), filepath.Clean(right.Path))
	}
	leftNames, leftErr := winDivertProcessNames(left)
	rightNames, rightErr := winDivertProcessNames(right)
	return leftErr == nil && rightErr == nil && len(leftNames) > 0 && len(rightNames) > 0 && strings.EqualFold(leftNames[0], rightNames[0])
}

func launchEntryIndex(entries []model.LaunchEntry, id string) int {
	for index := range entries {
		if entries[index].ID == id {
			return index
		}
	}
	return -1
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
