package main

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const mihomoLatestAPI = "https://api.github.com/repos/MetaCubeX/mihomo/releases/latest"

type MihomoProcess struct {
	cfg  MihomoConfig
	logs *LogBuffer

	mu      sync.Mutex
	cmd     *exec.Cmd
	running bool
}

type LogBuffer struct {
	mu    sync.Mutex
	lines []string
	limit int
}

func NewLogBuffer(limit int) *LogBuffer {
	return &LogBuffer{limit: limit}
}

func (b *LogBuffer) Add(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if strings.TrimSpace(line) == "" {
		return
	}
	b.lines = append(b.lines, time.Now().Format("15:04:05")+" "+line)
	if len(b.lines) > b.limit {
		b.lines = b.lines[len(b.lines)-b.limit:]
	}
}

func (b *LogBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	return out
}

func NewMihomoProcess(cfg MihomoConfig, logs *LogBuffer) *MihomoProcess {
	return &MihomoProcess{cfg: cfg, logs: logs}
}

func (p *MihomoProcess) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}
	if _, err := os.Stat(p.cfg.ExecutablePath); err != nil {
		return fmt.Errorf("mihomo executable not found: %s", p.cfg.ExecutablePath)
	}

	args := []string{"-f", p.cfg.ConfigPath}
	cmd := exec.Command(p.cfg.ExecutablePath, args...)
	cmd.Dir = filepath.Dir(p.cfg.ConfigPath)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	p.cmd = cmd
	p.running = true
	p.logs.Add("[Mihomo] started pid=" + fmt.Sprintf("%d", cmd.Process.Pid))

	go p.scanPipe(stdout)
	go p.scanPipe(stderr)
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
		if err != nil {
			p.logs.Add("[Mihomo] stopped with error: " + err.Error())
		} else {
			p.logs.Add("[Mihomo] stopped")
		}
	}()
	return nil
}

func (p *MihomoProcess) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	p.logs.Add("[Mihomo] stopping")
	err := p.cmd.Process.Kill()
	p.running = false
	return err
}

func (p *MihomoProcess) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *MihomoProcess) scanPipe(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		p.logs.Add(scanner.Text())
	}
}

func GenerateMihomoConfig(cfg *AppConfig) string {
	var b strings.Builder
	allowLAN := "false"
	bindAddress := "127.0.0.1"
	if cfg.Mihomo.AllowLAN {
		allowLAN = "true"
		bindAddress = "0.0.0.0"
	}

	fmt.Fprintf(&b, "mixed-port: %d\n", cfg.Mihomo.MixedPort)
	fmt.Fprintf(&b, "socks-port: %d\n", cfg.Mihomo.SocksPort)
	fmt.Fprintf(&b, "allow-lan: %s\n", allowLAN)
	fmt.Fprintf(&b, "bind-address: %s\n", bindAddress)
	fmt.Fprintf(&b, "mode: rule\n")
	fmt.Fprintf(&b, "log-level: info\n")
	fmt.Fprintf(&b, "find-process-mode: strict\n")
	fmt.Fprintf(&b, "external-controller: 127.0.0.1:%d\n", cfg.Mihomo.ControllerPort)
	fmt.Fprintf(&b, "external-controller-cors:\n")
	fmt.Fprintf(&b, "  allow-origins:\n")
	fmt.Fprintf(&b, "    - '*'\n")
	fmt.Fprintf(&b, "  allow-private-network: true\n")
	fmt.Fprintf(&b, "external-ui: ui\n")
	fmt.Fprintf(&b, "external-ui-name: xd\n")
	fmt.Fprintf(&b, "external-ui-url: \"https://github.com/MetaCubeX/metacubexd/archive/refs/heads/gh-pages.zip\"\n")
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "tun:\n")
	fmt.Fprintf(&b, "  enable: %s\n", boolText(cfg.Mihomo.TUNEnabled))
	fmt.Fprintf(&b, "  stack: mixed\n")
	fmt.Fprintf(&b, "  auto-route: true\n")
	fmt.Fprintf(&b, "  auto-detect-interface: true\n")
	fmt.Fprintf(&b, "  strict-route: false\n")
	fmt.Fprintf(&b, "  dns-hijack:\n")
	fmt.Fprintf(&b, "    - any:53\n\n")

	fmt.Fprintf(&b, "dns:\n")
	fmt.Fprintf(&b, "  enable: true\n")
	fmt.Fprintf(&b, "  listen: 127.0.0.1:%d\n", cfg.Mihomo.DNSPort)
	fmt.Fprintf(&b, "  enhanced-mode: fake-ip\n")
	fmt.Fprintf(&b, "  fake-ip-range: 198.18.0.1/16\n")
	fmt.Fprintf(&b, "  nameserver:\n")
	fmt.Fprintf(&b, "    - 223.5.5.5\n")
	fmt.Fprintf(&b, "    - 119.29.29.29\n\n")

	proxyNames := collectProxyNames(cfg)
	if len(proxyNames) == 0 {
		fmt.Fprintf(&b, "proxies: []\n")
	} else {
		fmt.Fprintf(&b, "proxies:\n")
		for _, srv := range cfg.EasyNetServers {
			if !srv.Enabled {
				continue
			}
			fmt.Fprintf(&b, "  - name: %s\n", yamlQuote(srv.Name))
			fmt.Fprintf(&b, "    type: socks5\n")
			fmt.Fprintf(&b, "    server: 127.0.0.1\n")
			fmt.Fprintf(&b, "    port: %d\n", srv.LocalPort)
			fmt.Fprintf(&b, "    udp: false\n")
		}
		for _, socks := range cfg.ExternalSocks5 {
			if !socks.Enabled {
				continue
			}
			fmt.Fprintf(&b, "  - name: %s\n", yamlQuote(socks.Name))
			fmt.Fprintf(&b, "    type: socks5\n")
			fmt.Fprintf(&b, "    server: %s\n", yamlQuote(socks.Host))
			fmt.Fprintf(&b, "    port: %d\n", socks.Port)
			if socks.Username != "" {
				fmt.Fprintf(&b, "    username: %s\n", yamlQuote(socks.Username))
			}
			if socks.Password != "" {
				fmt.Fprintf(&b, "    password: %s\n", yamlQuote(socks.Password))
			}
			fmt.Fprintf(&b, "    udp: %s\n", boolText(socks.UDP))
		}
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "proxy-groups:\n")
	fmt.Fprintf(&b, "  - name: PROXY\n")
	fmt.Fprintf(&b, "    type: select\n")
	fmt.Fprintf(&b, "    proxies:\n")
	for _, name := range proxyNames {
		fmt.Fprintf(&b, "      - %s\n", yamlQuote(name))
	}
	fmt.Fprintf(&b, "      - DIRECT\n")
	for _, chain := range cfg.Chains {
		if !chain.Enabled || len(chain.Proxies) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  - name: %s\n", yamlQuote(chain.Name))
		fmt.Fprintf(&b, "    type: relay\n")
		fmt.Fprintf(&b, "    proxies:\n")
		for _, name := range chain.Proxies {
			if strings.TrimSpace(name) != "" {
				fmt.Fprintf(&b, "      - %s\n", yamlQuote(strings.TrimSpace(name)))
			}
		}
	}
	fmt.Fprintf(&b, "\n")

	hasListener := false
	for _, chain := range cfg.Chains {
		if chain.Enabled && chain.ListenPort > 0 && len(chain.Proxies) > 0 {
			hasListener = true
			break
		}
	}
	if hasListener {
		fmt.Fprintf(&b, "listeners:\n")
		for _, chain := range cfg.Chains {
			if !chain.Enabled || chain.ListenPort <= 0 || len(chain.Proxies) == 0 {
				continue
			}
			fmt.Fprintf(&b, "  - name: %s\n", yamlQuote(chain.ID+"-in"))
			fmt.Fprintf(&b, "    type: socks\n")
			fmt.Fprintf(&b, "    listen: 127.0.0.1\n")
			fmt.Fprintf(&b, "    port: %d\n", chain.ListenPort)
			fmt.Fprintf(&b, "    proxy: %s\n", yamlQuote(chain.Name))
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "rules:\n")
	fmt.Fprintf(&b, "  - PROCESS-NAME,mihomo.exe,DIRECT\n")
	fmt.Fprintf(&b, "  - PROCESS-NAME,proxy-go.exe,DIRECT\n")
	fmt.Fprintf(&b, "  - PROCESS-NAME,proxy-go-silent.exe,DIRECT\n")
	fmt.Fprintf(&b, "  - PROCESS-NAME,easy-net-manager.exe,DIRECT\n")
	for _, rule := range cfg.ProcessRules {
		if strings.TrimSpace(rule.ProcessName) == "" {
			continue
		}
		policy := strings.TrimSpace(rule.Policy)
		if policy == "" {
			policy = "PROXY"
		}
		fmt.Fprintf(&b, "  - PROCESS-NAME,%s,%s\n", strings.TrimSpace(rule.ProcessName), policy)
	}
	fmt.Fprintf(&b, "  - MATCH,DIRECT\n")
	return b.String()
}

func WriteMihomoConfig(path string, yamlText string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(yamlText), 0644)
}

func UpgradeMihomoUI(port int) error {
	client := &http.Client{Timeout: 90 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/upgrade/ui", port)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mihomo ui upgrade returned %s: %s", resp.Status, string(body))
	}
	return nil
}

func DownloadMihomo(workDir string, logs *LogBuffer) (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("the built-in downloader currently selects Windows assets only")
	}

	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest(http.MethodGet, mihomoLatestAPI, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "easy-net-manager")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("GitHub release API returned %s", resp.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	assetName, assetURL := chooseWindowsAsset(release.Assets)
	if assetURL == "" {
		return "", fmt.Errorf("no Windows AMD64 zip asset found in release %s", release.TagName)
	}
	logs.Add("[Mihomo] downloading " + assetName)

	mihomoDir := filepath.Join(workDir, "mihomo")
	if err := os.MkdirAll(mihomoDir, 0755); err != nil {
		return "", err
	}
	zipPath := filepath.Join(mihomoDir, assetName)
	if err := downloadFile(client, assetURL, zipPath); err != nil {
		return "", err
	}

	exePath, err := extractMihomoExe(zipPath, filepath.Join(mihomoDir, "mihomo.exe"))
	if err != nil {
		return "", err
	}
	logs.Add("[Mihomo] ready at " + exePath)
	return exePath, nil
}

func chooseWindowsAsset(assets []struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}) (string, string) {
	preferred := []string{
		"windows-amd64-v1-",
		"windows-amd64-compatible-",
		"windows-amd64-",
	}
	for _, needle := range preferred {
		for _, asset := range assets {
			name := strings.ToLower(asset.Name)
			if strings.Contains(name, needle) && strings.HasSuffix(name, ".zip") {
				return asset.Name, asset.URL
			}
		}
	}
	return "", ""
}

func downloadFile(client *http.Client, url string, path string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "easy-net-manager")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("download returned %s", resp.Status)
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func extractMihomoExe(zipPath string, targetPath string) (string, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	for _, file := range reader.File {
		if file.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(file.Name), ".exe") {
			continue
		}
		src, err := file.Open()
		if err != nil {
			return "", err
		}
		defer src.Close()
		dst, err := os.Create(targetPath)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(dst, src); err != nil {
			_ = dst.Close()
			return "", err
		}
		if err := dst.Close(); err != nil {
			return "", err
		}
		return targetPath, nil
	}
	return "", fmt.Errorf("no .exe found in %s", zipPath)
}

func collectProxyNames(cfg *AppConfig) []string {
	names := make([]string, 0)
	for _, srv := range cfg.EasyNetServers {
		if srv.Enabled {
			names = append(names, srv.Name)
		}
	}
	for _, socks := range cfg.ExternalSocks5 {
		if socks.Enabled {
			names = append(names, socks.Name)
		}
	}
	for _, chain := range cfg.Chains {
		if chain.Enabled && len(chain.Proxies) > 0 {
			names = append(names, chain.Name)
		}
	}
	sort.Strings(names)
	return names
}

func yamlQuote(s string) string {
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return "\"" + escaped + "\""
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func logError(prefix string, err error) {
	if err != nil {
		log.Printf("%s: %v", prefix, err)
	}
}
