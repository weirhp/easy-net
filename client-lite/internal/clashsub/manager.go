package clashsub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"easy-net/client-lite/internal/model"
)

type Fetcher func(url string) ([]byte, error)

type Manager struct {
	mu          sync.Mutex
	dir         string
	store       *store
	file        *model.SubscriptionFile
	runner      Runner
	fetch       Fetcher
	refreshHook func(id string) error
}

func New(dir string, runner Runner) (*Manager, error) {
	if runner == nil {
		runner = DefaultRunner(dir)
	}
	manager := &Manager{dir: dir, store: newStore(dir), runner: runner, fetch: Fetch}
	file, err := manager.store.Load()
	if err != nil {
		return nil, err
	}
	manager.file = file
	return manager, nil
}

func (m *Manager) SetFetcher(fetch Fetcher) {
	if fetch != nil {
		m.fetch = fetch
	}
}

func (m *Manager) List() []model.Subscription {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]model.Subscription, 0, len(m.file.Subscriptions))
	for _, item := range m.file.Subscriptions {
		out = append(out, item.Clone())
	}
	return out
}

func (m *Manager) Get(id string) (model.Subscription, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexLocked(id)
	if index < 0 {
		return model.Subscription{}, false
	}
	return m.file.Subscriptions[index].Clone(), true
}

func (m *Manager) SetRefreshHook(hook func(id string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshHook = hook
}

func (m *Manager) Import(name, rawURL string, listenPort, refreshMinutes int, bypassPrivate, bypassChina bool) (model.Subscription, error) {
	name = strings.TrimSpace(name)
	rawURL = strings.TrimSpace(rawURL)
	if name == "" || len([]rune(name)) > 40 {
		return model.Subscription{}, fmt.Errorf("订阅名称无效")
	}
	if rawURL == "" {
		return model.Subscription{}, fmt.Errorf("订阅地址不能为空")
	}
	nodes, err := m.downloadNodes(rawURL)
	if err != nil {
		return model.Subscription{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.file.Subscriptions) >= model.MaxClashSubscriptions {
		return model.Subscription{}, fmt.Errorf("Clash 订阅最多 %d 个", model.MaxClashSubscriptions)
	}
	for _, existing := range m.file.Subscriptions {
		if strings.EqualFold(existing.Name, name) {
			return model.Subscription{}, fmt.Errorf("已存在同名订阅 Tab「%s」", name)
		}
		if existing.URL == rawURL {
			return model.Subscription{}, fmt.Errorf("该订阅地址已经导入")
		}
	}
	sub := model.Subscription{
		ID: newID(), Name: name, URL: rawURL, ListenPort: listenPort,
		RefreshMinutes: model.NormalizeRefreshMinutes(refreshMinutes),
		BypassPrivate:  bypassPrivate, BypassChina: bypassChina,
		Nodes: nodes, UpdatedAt: time.Now(),
	}
	sub.Normalize()
	if err := sub.Validate(); err != nil {
		return model.Subscription{}, err
	}
	m.file.Subscriptions = append(m.file.Subscriptions, sub)
	if err := m.store.Save(m.file); err != nil {
		return model.Subscription{}, err
	}
	return sub.Clone(), nil
}

func (m *Manager) Refresh(id string) (model.Subscription, error) {
	sub, ok := m.Get(id)
	if !ok {
		return model.Subscription{}, fmt.Errorf("Clash 订阅不存在")
	}
	nodes, err := m.downloadNodes(sub.URL)
	if err != nil {
		return model.Subscription{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexLocked(id)
	if index < 0 {
		return model.Subscription{}, fmt.Errorf("Clash 订阅不存在")
	}
	current := m.file.Subscriptions[index]
	current.Nodes = nodes
	current.UpdatedAt = time.Now()
	current.Normalize()
	if current.SelectedNode == "" {
		_ = m.runner.Stop(current.ID)
		current.Active = false
	}
	m.file.Subscriptions[index] = current
	if err := m.store.Save(m.file); err != nil {
		return model.Subscription{}, err
	}
	return current.Clone(), nil
}

func (m *Manager) SetRefreshInterval(id string, refreshMinutes int) (model.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexLocked(id)
	if index < 0 {
		return model.Subscription{}, fmt.Errorf("Clash 订阅不存在")
	}
	m.file.Subscriptions[index].RefreshMinutes = model.NormalizeRefreshMinutes(refreshMinutes)
	if err := m.store.Save(m.file); err != nil {
		return model.Subscription{}, err
	}
	return m.file.Subscriptions[index].Clone(), nil
}

func (m *Manager) SetBypass(id string, bypassPrivate, bypassChina bool) (model.Subscription, error) {
	m.mu.Lock()
	index := m.indexLocked(id)
	if index < 0 {
		m.mu.Unlock()
		return model.Subscription{}, fmt.Errorf("Clash 订阅不存在")
	}
	m.file.Subscriptions[index].BypassPrivate = bypassPrivate
	m.file.Subscriptions[index].BypassChina = bypassChina
	current := m.file.Subscriptions[index].Clone()
	if err := m.store.Save(m.file); err != nil {
		m.mu.Unlock()
		return model.Subscription{}, err
	}
	shouldRestart := current.Active && current.SelectedNode != "" && m.runner.Running(current.ID)
	m.mu.Unlock()
	if shouldRestart {
		if err := m.StartNode(current.ID, current.SelectedNode); err != nil {
			return current, fmt.Errorf("直连规则已保存，但重启节点失败：%w", err)
		}
	}
	return current, nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexLocked(id)
	if index < 0 {
		return fmt.Errorf("Clash 订阅不存在")
	}
	_ = m.runner.Stop(id)
	m.file.Subscriptions = append(m.file.Subscriptions[:index], m.file.Subscriptions[index+1:]...)
	return m.store.Save(m.file)
}

func (m *Manager) StartNode(id, nodeName string) error {
	sub, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("Clash 订阅不存在")
	}
	node, ok := sub.Node(nodeName)
	if !ok {
		return fmt.Errorf("订阅中找不到节点 %q", nodeName)
	}
	if err := m.runner.Start(sub.ID, sub.ListenPort, node.Raw, sub.BypassPrivate, sub.BypassChina); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexLocked(id)
	if index < 0 {
		return fmt.Errorf("Clash 订阅不存在")
	}
	m.file.Subscriptions[index].SelectedNode = node.Name
	m.file.Subscriptions[index].Active = true
	if err := m.store.Save(m.file); err != nil {
		_ = m.runner.Stop(id)
		return err
	}
	return nil
}

func (m *Manager) Stop(id string) error {
	id = strings.TrimPrefix(strings.TrimSpace(id), "clash-")
	if err := m.runner.Stop(id); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexLocked(id)
	if index < 0 {
		return nil
	}
	m.file.Subscriptions[index].Active = false
	return m.store.Save(m.file)
}

func (m *Manager) Running(id string) bool {
	return m.runner.Running(strings.TrimPrefix(strings.TrimSpace(id), "clash-"))
}

// StartMonitor restores nodes that were active before Lite restarted and
// restarts a node if its owned mihomo process exits unexpectedly. Manual Stop
// clears Active, so it is never mistaken for a crash.
func (m *Manager) StartMonitor(ctx context.Context, report func(string, ...any)) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		lastAttempt := make(map[string]time.Time)
		failures := make(map[string]int)
		lastRefreshAttempt := make(map[string]time.Time)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, sub := range m.List() {
					m.maybeAutoRefresh(sub, lastRefreshAttempt, report)
					if !sub.Active || sub.SelectedNode == "" || m.Running(sub.ID) {
						continue
					}
					delay := 5 * time.Second
					if failures[sub.ID] > 0 {
						delay = time.Duration(1<<min(failures[sub.ID], 5)) * 5 * time.Second
					}
					if time.Since(lastAttempt[sub.ID]) < delay {
						continue
					}
					lastAttempt[sub.ID] = time.Now()
					if err := m.StartNode(sub.ID, sub.SelectedNode); err != nil {
						failures[sub.ID]++
						if report != nil {
							report("[Easy-Net Lite] 自动恢复 Clash 节点 %s 失败：%v", sub.Name, err)
						}
					} else {
						failures[sub.ID] = 0
					}
				}
			}
		}
	}()
}

func (m *Manager) maybeAutoRefresh(sub model.Subscription, lastAttempt map[string]time.Time, report func(string, ...any)) {
	if !sub.RefreshDue() {
		return
	}
	if !lastAttempt[sub.ID].IsZero() && time.Since(lastAttempt[sub.ID]) < 5*time.Minute {
		return
	}
	m.mu.Lock()
	hook := m.refreshHook
	m.mu.Unlock()
	if hook == nil {
		return
	}
	lastAttempt[sub.ID] = time.Now()
	go func(id, name string) {
		if err := hook(id); err != nil && report != nil {
			report("[Easy-Net Lite] 自动刷新订阅 %s 失败：%v", name, err)
		}
	}(sub.ID, sub.Name)
}

func (m *Manager) downloadNodes(rawURL string) ([]model.ClashNode, error) {
	fetch := m.fetch
	if fetch == nil {
		fetch = Fetch
	}
	data, err := fetch(rawURL)
	if err != nil {
		return nil, err
	}
	nodes, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("订阅中没有可用的 Clash 节点")
	}
	return nodes, nil
}

func (m *Manager) indexLocked(id string) int {
	id = strings.TrimSpace(id)
	for index, item := range m.file.Subscriptions {
		if item.ID == id {
			return index
		}
	}
	return -1
}

func ProfileID(subscriptionID string) string {
	return "clash-" + subscriptionID
}

type View struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	URL            string     `json:"url"`
	ListenAddress  string     `json:"listenAddress"`
	SelectedNode   string     `json:"selectedNode,omitempty"`
	UpdatedAt      string     `json:"updatedAt,omitempty"`
	RefreshMinutes int        `json:"refreshMinutes"`
	BypassPrivate  bool       `json:"bypassPrivate"`
	BypassChina    bool       `json:"bypassChina"`
	Running        bool       `json:"running"`
	ProfileID      string     `json:"profileId"`
	ProfileDefault bool       `json:"profileDefault"`
	Error          string     `json:"error,omitempty"`
	Nodes          []NodeView `json:"nodes"`
}

type NodeView struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Server string `json:"server,omitempty"`
	Port   int    `json:"port,omitempty"`
}

func newID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}
