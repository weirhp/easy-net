package main

import (
	"fmt"
	"log"
	"sync"
)

type Manager struct {
	workDir string
	store   *ConfigStore

	mu             sync.Mutex
	cfg            *AppConfig
	easyNet        map[string]*SocksServer
	easyNetErrors  map[string]string
	traffic        *TrafficStats
	mihomo         *MihomoProcess
	mihomoLogs     *LogBuffer
	lastMihomoYAML string
}

type RuntimeState struct {
	Config             *AppConfig                 `json:"config"`
	EasyNet            map[string]string          `json:"easyNet"`
	EasyNetErr         map[string]string          `json:"easyNetErrors"`
	EasyNetTraffic     map[string]TrafficSnapshot `json:"easyNetTraffic"`
	Mihomo             MihomoRuntimeState         `json:"mihomo"`
	MihomoYAML         string                     `json:"mihomoYaml"`
	DirectProcessNames []string                   `json:"directProcessNames"`
}

type MihomoRuntimeState struct {
	Running    bool     `json:"running"`
	Executable string   `json:"executable"`
	ConfigPath string   `json:"configPath"`
	Logs       []string `json:"logs"`
}

func NewManager(workDir string, store *ConfigStore, cfg *AppConfig) *Manager {
	return &Manager{
		workDir:       workDir,
		store:         store,
		cfg:           cfg,
		easyNet:       make(map[string]*SocksServer),
		easyNetErrors: make(map[string]string),
		traffic:       NewTrafficStats(workDir),
		mihomoLogs:    NewLogBuffer(120),
	}
}

func (m *Manager) ApplyStartup() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.applyLocked()
}

func (m *Manager) SaveAndApply(cfg *AppConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.store.Save(cfg); err != nil {
		return err
	}
	m.cfg = cfg
	return m.applyLocked()
}

func (m *Manager) State() RuntimeState {
	m.mu.Lock()
	defer m.mu.Unlock()

	easyState := make(map[string]string)
	easyErrors := make(map[string]string)
	easyIDs := make([]string, 0, len(m.cfg.EasyNetServers))
	for _, srvCfg := range m.cfg.EasyNetServers {
		easyIDs = append(easyIDs, srvCfg.ID)
		status := "stopped"
		if srv := m.easyNet[srvCfg.ID]; srv != nil && srv.Running() {
			status = "running"
		}
		easyState[srvCfg.ID] = status
		if errText := m.easyNetErrors[srvCfg.ID]; errText != "" {
			easyErrors[srvCfg.ID] = errText
		}
	}

	running := false
	if m.mihomo != nil {
		running = m.mihomo.Running()
	}
	yamlText := m.lastMihomoYAML
	if yamlText == "" {
		yamlText = GenerateMihomoConfig(m.cfg)
	}

	return RuntimeState{
		Config:             m.cfg,
		EasyNet:            easyState,
		EasyNetErr:         easyErrors,
		EasyNetTraffic:     m.traffic.SnapshotEasyNet(easyIDs),
		MihomoYAML:         yamlText,
		DirectProcessNames: directProcessNames(),
		Mihomo: MihomoRuntimeState{
			Running:    running,
			Executable: m.cfg.Mihomo.ExecutablePath,
			ConfigPath: m.cfg.Mihomo.ConfigPath,
			Logs:       m.mihomoLogs.Lines(),
		},
	}
}

func (m *Manager) StartEasyNet(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, ok := m.findEasyNetLocked(id)
	if !ok {
		return fmt.Errorf("easy-net server not found: %s", id)
	}
	if err := m.startEasyNetLocked(cfg); err != nil {
		m.easyNetErrors[id] = err.Error()
		return err
	}
	m.easyNetErrors[id] = ""
	m.setEasyNetEnabledLocked(id, true)
	return m.store.Save(m.cfg)
}

func (m *Manager) StopEasyNet(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv, ok := m.easyNet[id]
	if !ok {
		m.setEasyNetEnabledLocked(id, false)
		return m.store.Save(m.cfg)
	}
	srv.Stop()
	delete(m.easyNet, id)
	m.easyNetErrors[id] = ""
	m.setEasyNetEnabledLocked(id, false)
	return m.store.Save(m.cfg)
}

func (m *Manager) StartMihomo() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startMihomoLocked()
}

func (m *Manager) StopMihomo() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopMihomoLocked()
}

func (m *Manager) RestartMihomo() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.stopMihomoLocked(); err != nil {
		return err
	}
	return m.startMihomoLocked()
}

func (m *Manager) GenerateMihomoFile() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writeMihomoConfigLocked()
}

func (m *Manager) DownloadMihomo() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	path, err := DownloadMihomo(m.workDir, m.mihomoLogs)
	if err != nil {
		return "", err
	}
	m.cfg.Mihomo.ExecutablePath = path
	if err := m.store.Save(m.cfg); err != nil {
		return "", err
	}
	return path, nil
}

func (m *Manager) UpgradeMihomoUI() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.mihomo == nil || !m.mihomo.Running() {
		return fmt.Errorf("请先启动 Mihomo 内核，再更新面板")
	}
	return UpgradeMihomoUI(m.cfg.Mihomo.ControllerPort)
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.stopMihomoLocked(); err != nil {
		log.Printf("[Easy-Net] stop mihomo warning: %v", err)
	}
	for id, srv := range m.easyNet {
		srv.Stop()
		delete(m.easyNet, id)
	}
	m.traffic.Save()
}

func (m *Manager) applyLocked() error {
	for _, srvCfg := range m.cfg.EasyNetServers {
		if srvCfg.Enabled {
			if err := m.startEasyNetLocked(srvCfg); err != nil {
				m.easyNetErrors[srvCfg.ID] = err.Error()
				return err
			}
			m.easyNetErrors[srvCfg.ID] = ""
			continue
		}
		if srv := m.easyNet[srvCfg.ID]; srv != nil {
			srv.Stop()
			delete(m.easyNet, srvCfg.ID)
		}
		m.easyNetErrors[srvCfg.ID] = ""
	}

	valid := make(map[string]bool)
	for _, srvCfg := range m.cfg.EasyNetServers {
		valid[srvCfg.ID] = true
	}
	for id, srv := range m.easyNet {
		if !valid[id] {
			srv.Stop()
			delete(m.easyNet, id)
		}
	}

	if m.cfg.Mihomo.Enabled {
		if err := m.startMihomoLocked(); err != nil {
			return err
		}
	} else {
		if err := m.stopMihomoLocked(); err != nil {
			return err
		}
		if err := m.writeMihomoConfigLocked(); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) startEasyNetLocked(cfg EasyNetConfig) error {
	if srv := m.easyNet[cfg.ID]; srv != nil && srv.Running() {
		return nil
	}
	srv := NewSocksServer(cfg, m.traffic.AddEasyNet)
	if err := srv.Start(); err != nil {
		return err
	}
	m.easyNet[cfg.ID] = srv
	log.Printf("[Easy-Net] socks server started: %s 127.0.0.1:%d", cfg.ID, cfg.LocalPort)
	return nil
}

func (m *Manager) startMihomoLocked() error {
	if m.mihomo != nil && m.mihomo.Running() {
		return nil
	}
	if m.cfg.Mihomo.TUNEnabled && !hasTunPrivileges() {
		return fmt.Errorf("Mihomo TUN 模式需要管理员权限，请右键以管理员身份运行 easy-net-manager.exe 后再启动；如果只想用本地 Mixed/SOCKS 端口，可以先关闭 TUN 模式")
	}
	for _, srvCfg := range m.cfg.EasyNetServers {
		if srvCfg.Enabled {
			if err := m.startEasyNetLocked(srvCfg); err != nil {
				return err
			}
		}
	}
	if err := m.writeMihomoConfigLocked(); err != nil {
		return err
	}
	p := NewMihomoProcess(m.cfg.Mihomo, m.mihomoLogs)
	if err := p.Start(); err != nil {
		return err
	}
	m.mihomo = p
	return nil
}

func (m *Manager) stopMihomoLocked() error {
	if m.mihomo == nil {
		return nil
	}
	err := m.mihomo.Stop()
	m.mihomo = nil
	return err
}

func (m *Manager) writeMihomoConfigLocked() error {
	yamlText := GenerateMihomoConfig(m.cfg)
	if m.cfg.Mihomo.RawConfig != "" {
		yamlText = m.cfg.Mihomo.RawConfig
	}
	if err := WriteMihomoConfig(m.cfg.Mihomo.ConfigPath, yamlText); err != nil {
		return err
	}
	m.lastMihomoYAML = yamlText
	return nil
}

func (m *Manager) findEasyNetLocked(id string) (EasyNetConfig, bool) {
	for _, cfg := range m.cfg.EasyNetServers {
		if cfg.ID == id {
			return cfg, true
		}
	}
	return EasyNetConfig{}, false
}

func (m *Manager) setEasyNetEnabledLocked(id string, enabled bool) {
	for i := range m.cfg.EasyNetServers {
		if m.cfg.EasyNetServers[i].ID == id {
			m.cfg.EasyNetServers[i].Enabled = enabled
			return
		}
	}
}
