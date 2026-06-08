package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ConfigStore struct {
	workDir string
	path    string
}

type AppConfig struct {
	ManagerPort    int              `json:"managerPort"`
	Mihomo         MihomoConfig     `json:"mihomo"`
	EasyNetServers []EasyNetConfig  `json:"easyNetServers"`
	ExternalSocks5 []ExternalSocks5 `json:"externalSocks5"`
	Chains         []ProxyChain     `json:"chains"`
	ProcessRules   []ProcessRule    `json:"processRules"`
}

type MihomoConfig struct {
	Enabled        bool   `json:"enabled"`
	ExecutablePath string `json:"executablePath"`
	ConfigPath     string `json:"configPath"`
	MixedPort      int    `json:"mixedPort"`
	SocksPort      int    `json:"socksPort"`
	ControllerPort int    `json:"controllerPort"`
	DNSPort        int    `json:"dnsPort"`
	TUNEnabled     bool   `json:"tunEnabled"`
	AllowLAN       bool   `json:"allowLan"`
	RawConfig      string `json:"rawConfig"`
}

type EasyNetConfig struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	WorkerHost string `json:"workerHost"`
	LocalPort  int    `json:"localPort"`
	Secret     string `json:"secret"`
	EndpointIP string `json:"endpointIP"`
}

type ExternalSocks5 struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	UDP      bool   `json:"udp"`
}

type ProxyChain struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Enabled    bool     `json:"enabled"`
	ListenPort int      `json:"listenPort"`
	Proxies    []string `json:"proxies"`
}

type ProcessRule struct {
	ProcessName string `json:"processName"`
	Policy      string `json:"policy"`
}

type legacyConfig struct {
	WorkerHost string `json:"workerHost"`
	LocalPort  int    `json:"localPort"`
	Secret     string `json:"secret"`
	EndpointIP string `json:"endpointIP"`
}

func NewConfigStore(workDir string) *ConfigStore {
	return &ConfigStore{
		workDir: workDir,
		path:    filepath.Join(workDir, "local-config.json"),
	}
}

func (s *ConfigStore) Path() string {
	return s.path
}

func (s *ConfigStore) Load() (*AppConfig, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := defaultConfig(s.workDir)
			return cfg, s.Save(cfg)
		}
		return nil, err
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}

	if _, ok := probe["workerHost"]; ok {
		var legacy legacyConfig
		if err := json.Unmarshal(data, &legacy); err != nil {
			return nil, err
		}
		cfg := defaultConfig(s.workDir)
		cfg.EasyNetServers = []EasyNetConfig{{
			ID:         "easy-net-" + portID(legacy.LocalPort),
			Name:       "Easy-Net " + portID(legacy.LocalPort),
			Enabled:    true,
			WorkerHost: legacy.WorkerHost,
			LocalPort:  legacy.LocalPort,
			Secret:     legacy.Secret,
			EndpointIP: legacy.EndpointIP,
		}}
		return cfg, s.Save(cfg)
	}

	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	normalizeConfig(&cfg, s.workDir)
	return &cfg, nil
}

func (s *ConfigStore) Save(cfg *AppConfig) error {
	normalizeConfig(cfg, s.workDir)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0644)
}

func defaultConfig(workDir string) *AppConfig {
	return &AppConfig{
		ManagerPort: 18080,
		Mihomo: MihomoConfig{
			Enabled:        false,
			ExecutablePath: filepath.Join(workDir, "mihomo", "mihomo.exe"),
			ConfigPath:     filepath.Join(workDir, "mihomo", "config.yaml"),
			MixedPort:      7890,
			SocksPort:      7891,
			ControllerPort: 9090,
			DNSPort:        1053,
			TUNEnabled:     true,
			AllowLAN:       false,
		},
		EasyNetServers: []EasyNetConfig{{
			ID:         "easy-net-1080",
			Name:       "Easy-Net 1080",
			Enabled:    false,
			WorkerHost: "your-server-domain.com",
			LocalPort:  1080,
			Secret:     "easy-net-secret-key-12345",
		}},
		ExternalSocks5: []ExternalSocks5{},
		Chains:         []ProxyChain{},
		ProcessRules: []ProcessRule{
			{ProcessName: "Telegram.exe", Policy: "PROXY"},
			{ProcessName: "Discord.exe", Policy: "PROXY"},
		},
	}
}

func normalizeConfig(cfg *AppConfig, workDir string) {
	if cfg.ManagerPort == 0 {
		cfg.ManagerPort = 18080
	}
	if cfg.Mihomo.ExecutablePath == "" {
		cfg.Mihomo.ExecutablePath = filepath.Join(workDir, "mihomo", "mihomo.exe")
	}
	if cfg.Mihomo.ConfigPath == "" {
		cfg.Mihomo.ConfigPath = filepath.Join(workDir, "mihomo", "config.yaml")
	}
	if cfg.Mihomo.MixedPort == 0 {
		cfg.Mihomo.MixedPort = 7890
	}
	if cfg.Mihomo.SocksPort == 0 {
		cfg.Mihomo.SocksPort = 7891
	}
	if cfg.Mihomo.ControllerPort == 0 {
		cfg.Mihomo.ControllerPort = 9090
	}
	if cfg.Mihomo.DNSPort == 0 {
		cfg.Mihomo.DNSPort = 1053
	}

	for i := range cfg.EasyNetServers {
		if cfg.EasyNetServers[i].LocalPort == 0 {
			cfg.EasyNetServers[i].LocalPort = 1080 + i
		}
		if cfg.EasyNetServers[i].ID == "" {
			cfg.EasyNetServers[i].ID = "easy-net-" + portID(cfg.EasyNetServers[i].LocalPort)
		}
		if cfg.EasyNetServers[i].Name == "" {
			cfg.EasyNetServers[i].Name = "Easy-Net " + portID(cfg.EasyNetServers[i].LocalPort)
		}
		cfg.EasyNetServers[i].WorkerHost = strings.TrimSpace(cfg.EasyNetServers[i].WorkerHost)
	}
	for i := range cfg.ExternalSocks5 {
		if cfg.ExternalSocks5[i].ID == "" {
			cfg.ExternalSocks5[i].ID = "socks5-" + portID(cfg.ExternalSocks5[i].Port)
		}
		if cfg.ExternalSocks5[i].Name == "" {
			cfg.ExternalSocks5[i].Name = cfg.ExternalSocks5[i].ID
		}
		cfg.ExternalSocks5[i].Host = strings.TrimSpace(cfg.ExternalSocks5[i].Host)
	}
	for i := range cfg.Chains {
		if cfg.Chains[i].ID == "" {
			cfg.Chains[i].ID = "chain-" + portID(cfg.Chains[i].ListenPort)
		}
		if cfg.Chains[i].Name == "" {
			cfg.Chains[i].Name = cfg.Chains[i].ID
		}
	}
	for i := range cfg.ProcessRules {
		if cfg.ProcessRules[i].Policy == "" {
			cfg.ProcessRules[i].Policy = "PROXY"
		}
	}
}

func portID(port int) string {
	if port == 0 {
		return "new"
	}
	return fmt.Sprintf("%d", port)
}
