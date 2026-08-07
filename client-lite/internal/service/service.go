package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/proxy"
	"easy-net/client-lite/internal/secretstore"
	"easy-net/client-lite/internal/sharecode"
	"easy-net/client-lite/internal/transport"
	sshtransport "easy-net/client-lite/internal/transport/ssh"
	websockettransport "easy-net/client-lite/internal/transport/websocket"
)

type SecretValues struct {
	WebSocketSecret string
	SSHPassword     string
	SSHPassphrase   string
	SSHPrivateKey   []byte
}

type ProfileState struct {
	Profile          model.Profile
	Running          bool
	Starting         bool
	Error            string
	ConnectionStatus string
	ConnectionError  string
	ConnectionAt     time.Time
}

type Service struct {
	mu           sync.Mutex
	configMu     sync.Mutex
	store        *config.Store
	secrets      secretstore.Store
	cfg          *model.Config
	instances    map[string]*proxy.Server
	errors       map[string]string
	starting     map[string]context.CancelFunc
	profileLocks map[string]*sync.Mutex
	connections  map[string]connectionHealth
	revisions    map[string]uint64
}

type connectionHealth struct {
	Status    string
	Error     string
	CheckedAt time.Time
}

func New(store *config.Store, secrets secretstore.Store) (*Service, error) {
	cfg, err := store.Load()
	if err != nil {
		return nil, err
	}
	return &Service{
		store: store, secrets: secrets, cfg: cfg,
		instances: make(map[string]*proxy.Server), errors: make(map[string]string),
		starting: make(map[string]context.CancelFunc), profileLocks: make(map[string]*sync.Mutex),
		connections: make(map[string]connectionHealth), revisions: make(map[string]uint64),
	}, nil
}

func (s *Service) ConfigPath() string { return s.store.Path() }

func (s *Service) ConfigWarnings() []string { return s.store.Warnings() }

func (s *Service) States() []ProfileState {
	s.mu.Lock()
	defer s.mu.Unlock()
	states := make([]ProfileState, 0, len(s.cfg.Profiles))
	for _, profile := range s.cfg.Profiles {
		server := s.instances[profile.ID]
		_, starting := s.starting[profile.ID]
		connection := s.connections[profile.ID]
		states = append(states, ProfileState{
			Profile: profile.Clone(), Running: server != nil && server.Running(), Starting: starting, Error: s.errors[profile.ID],
			ConnectionStatus: connection.Status, ConnectionError: connection.Error, ConnectionAt: connection.CheckedAt,
		})
	}
	sort.SliceStable(states, func(i, j int) bool { return states[i].Profile.Name < states[j].Profile.Name })
	return states
}

func (s *Service) Profile(id string) (model.Profile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, profile := range s.cfg.Profiles {
		if profile.ID == id {
			return profile.Clone(), true
		}
	}
	return model.Profile{}, false
}

func (s *Service) Secret(ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	return s.secrets.Get(ref)
}

func (s *Service) ExportShare(id string) (sharecode.Payload, error) {
	op := s.profileLock(id)
	op.Lock()
	defer op.Unlock()
	profile, ok := s.Profile(id)
	if !ok {
		return sharecode.Payload{}, fmt.Errorf("代理配置不存在")
	}
	payload := sharecode.Payload{Name: profile.Name, Type: profile.Type, PreferredPort: profile.ListenPort}
	switch profile.Type {
	case model.ProxyTypeWebSocket:
		secret, err := s.getSecret(profile.WebSocket.SecretRef, "WebSocket 密钥")
		if err != nil {
			return sharecode.Payload{}, err
		}
		payload.WebSocket = &sharecode.WebSocketConfig{
			URL: profile.WebSocket.URL, Secret: secret, AllowInsecure: profile.WebSocket.AllowInsecure, LegacyQueryAuth: profile.WebSocket.LegacyQueryAuth,
		}
	case model.ProxyTypeSSH:
		sharedSSH := &sharecode.SSHConfig{
			Host: profile.SSH.Host, Port: profile.SSH.Port, Username: profile.SSH.Username,
			AuthType: profile.SSH.AuthType, HostKeyFingerprint: profile.SSH.HostKeyFingerprint,
		}
		if profile.SSH.AuthType == model.AuthTypePassword {
			password, err := s.getSecret(profile.SSH.PasswordRef, "SSH 密码")
			if err != nil {
				return sharecode.Payload{}, err
			}
			sharedSSH.Password = password
		} else {
			privateKey, err := s.store.ReadManagedPrivateKey(profile.SSH.PrivateKeyPath)
			if err != nil {
				return sharecode.Payload{}, err
			}
			sharedSSH.PrivateKey = string(privateKey)
			passphrase, err := s.getOptionalSecret(profile.SSH.PassphraseRef, "私钥口令")
			if err != nil {
				return sharecode.Payload{}, err
			}
			sharedSSH.Passphrase = passphrase
		}
		payload.SSH = sharedSSH
	default:
		return sharecode.Payload{}, fmt.Errorf("不支持的代理类型")
	}
	if err := sharecode.Validate(payloadWithVersion(payload)); err != nil {
		return sharecode.Payload{}, err
	}
	return payload, nil
}

func (s *Service) ImportShare(payload sharecode.Payload) (string, error) {
	if err := sharecode.Validate(payload); err != nil {
		return "", err
	}
	id := newID()
	profile := model.Profile{
		ID: id, Name: strings.TrimSpace(payload.Name), Type: payload.Type, ListenHost: "127.0.0.1",
		ListenPort: s.nextAvailablePort(payload.PreferredPort), AutoStart: false,
	}
	values := SecretValues{}
	if payload.WebSocket != nil {
		profile.WebSocket = &model.WebSocketConfig{URL: payload.WebSocket.URL, AllowInsecure: payload.WebSocket.AllowInsecure, LegacyQueryAuth: payload.WebSocket.LegacyQueryAuth}
		values.WebSocketSecret = payload.WebSocket.Secret
	}
	if payload.SSH != nil {
		profile.SSH = &model.SSHConfig{Host: payload.SSH.Host, Port: payload.SSH.Port, Username: payload.SSH.Username, AuthType: payload.SSH.AuthType}
		values.SSHPassword = payload.SSH.Password
		values.SSHPrivateKey = []byte(payload.SSH.PrivateKey)
		values.SSHPassphrase = payload.SSH.Passphrase
	}
	if err := s.Upsert(profile, values); err != nil {
		return "", err
	}
	if payload.SSH != nil && payload.SSH.HostKeyFingerprint != "" {
		if err := s.TrustSSHHost(id, payload.SSH.HostKeyFingerprint); err != nil {
			if rollbackErr := s.Delete(id); rollbackErr != nil {
				return "", fmt.Errorf("导入 SSH 指纹失败：%v；回滚导入配置也失败：%w", err, rollbackErr)
			}
			return "", fmt.Errorf("导入 SSH 指纹失败：%w", err)
		}
	}
	return id, nil
}

func payloadWithVersion(payload sharecode.Payload) sharecode.Payload {
	payload.Version = sharecode.CurrentVersion
	return payload
}

func (s *Service) nextAvailablePort(preferred int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	used := make(map[int]struct{}, len(s.cfg.Profiles))
	for _, profile := range s.cfg.Profiles {
		used[profile.ListenPort] = struct{}{}
	}
	if preferred < 1 || preferred > 65535 {
		preferred = 1080
	}
	for port := preferred; port <= 65535; port++ {
		if _, exists := used[port]; !exists {
			return port
		}
	}
	for port := 1080; port < preferred; port++ {
		if _, exists := used[port]; !exists {
			return port
		}
	}
	return preferred
}

func (s *Service) Upsert(incoming model.Profile, values SecretValues) error {
	if incoming.ID == "" {
		incoming.ID = newID()
	}
	s.cancelStart(incoming.ID)
	op := s.profileLock(incoming.ID)
	op.Lock()
	opHeld := true
	defer func() {
		if opHeld {
			op.Unlock()
		}
	}()

	s.configMu.Lock()
	configHeld := true
	defer func() {
		if configHeld {
			s.configMu.Unlock()
		}
	}()

	s.mu.Lock()
	updated := cloneConfig(s.cfg)
	oldIndex := profileIndex(updated, incoming.ID)
	var old *model.Profile
	var wasRunning bool
	if oldIndex >= 0 {
		oldCopy := updated.Profiles[oldIndex].Clone()
		old = &oldCopy
		server := s.instances[incoming.ID]
		wasRunning = server != nil && server.Running()
	}
	s.mu.Unlock()

	profile, newPrivateKeyPath, err := s.prepareProfile(incoming, old, values)
	if err != nil {
		return err
	}
	cleanupNewKey := func() {
		if newPrivateKeyPath != "" {
			_ = s.store.DeleteManagedPrivateKey(newPrivateKeyPath)
		}
	}

	if err := profile.Validate(); err != nil {
		cleanupNewKey()
		return err
	}
	for _, existing := range updated.Profiles {
		if existing.ID != profile.ID && existing.ListenAddress() == profile.ListenAddress() {
			cleanupNewKey()
			return fmt.Errorf("本地端口 %d 已被配置 %q 使用", profile.ListenPort, existing.Name)
		}
	}

	rollbackSecrets, err := s.saveSecretsTransactional(profile, values)
	if err != nil {
		cleanupNewKey()
		return err
	}
	rollback := func() {
		rollbackSecrets()
		cleanupNewKey()
	}
	if profile.WebSocket != nil {
		if err := s.requireSecret(profile.WebSocket.SecretRef, "WebSocket 密钥"); err != nil {
			rollback()
			return err
		}
	}
	if profile.SSH != nil && profile.SSH.AuthType == model.AuthTypePassword {
		if err := s.requireSecret(profile.SSH.PasswordRef, "SSH 密码"); err != nil {
			rollback()
			return err
		}
	}
	if oldIndex >= 0 {
		updated.Profiles[oldIndex] = profile
	} else {
		updated.Profiles = append(updated.Profiles, profile)
	}
	if err := s.store.Save(updated); err != nil {
		rollback()
		return err
	}

	s.mu.Lock()
	s.cfg = updated
	oldServer := s.instances[profile.ID]
	delete(s.instances, profile.ID)
	delete(s.starting, profile.ID)
	delete(s.connections, profile.ID)
	s.revisions[profile.ID]++
	s.errors[profile.ID] = ""
	s.mu.Unlock()

	configHeld = false
	s.configMu.Unlock()
	if oldServer != nil {
		oldServer.Stop()
	}
	if old != nil {
		s.cleanupReplacedCredentials(*old, profile)
	}
	opHeld = false
	op.Unlock()
	if wasRunning {
		if err := s.Start(profile.ID); err != nil {
			return fmt.Errorf("配置已保存，但重新启动失败：%w", err)
		}
	}
	return nil
}

func (s *Service) prepareProfile(incoming model.Profile, old *model.Profile, values SecretValues) (model.Profile, string, error) {
	profile := incoming.Clone()
	profile.Normalize()
	if profile.Type == model.ProxyTypeWebSocket {
		profile.SSH = nil
		if profile.WebSocket == nil {
			profile.WebSocket = &model.WebSocketConfig{}
		}
		profile.WebSocket.SecretRef = profile.ID + "/websocket"
	} else if profile.Type == model.ProxyTypeSSH {
		profile.WebSocket = nil
		if profile.SSH == nil {
			profile.SSH = &model.SSHConfig{}
		}
		profile.SSH.PasswordRef = ""
		profile.SSH.PassphraseRef = ""
		if profile.SSH.AuthType == model.AuthTypePassword {
			profile.SSH.PasswordRef = profile.ID + "/password"
		} else if profile.SSH.AuthType == model.AuthTypePrivateKey {
			profile.SSH.PassphraseRef = profile.ID + "/passphrase"
		}
		profile.SSH.PrivateKeyPath = ""
		profile.SSH.HostKeyFingerprint = ""
		if old != nil && old.SSH != nil {
			if profile.SSH.Host == old.SSH.Host && profile.SSH.Port == old.SSH.Port {
				profile.SSH.HostKeyFingerprint = old.SSH.HostKeyFingerprint
			}
			if profile.SSH.AuthType == model.AuthTypePrivateKey && old.SSH.AuthType == model.AuthTypePrivateKey {
				profile.SSH.PrivateKeyPath = old.SSH.PrivateKeyPath
			}
		}
	}

	newPrivateKeyPath := ""
	if profile.SSH != nil && profile.SSH.AuthType == model.AuthTypePrivateKey && len(values.SSHPrivateKey) > 0 {
		path, err := s.store.SavePrivateKey(values.SSHPrivateKey)
		if err != nil {
			return model.Profile{}, "", err
		}
		newPrivateKeyPath = path
		profile.SSH.PrivateKeyPath = path
	}
	if profile.SSH != nil && profile.SSH.AuthType != model.AuthTypePrivateKey {
		profile.SSH.PrivateKeyPath = ""
	}
	return profile, newPrivateKeyPath, nil
}

func (s *Service) Delete(id string) error {
	s.cancelStart(id)
	op := s.profileLock(id)
	op.Lock()
	defer op.Unlock()
	s.configMu.Lock()
	defer s.configMu.Unlock()

	s.mu.Lock()
	updated := cloneConfig(s.cfg)
	index := profileIndex(updated, id)
	if index < 0 {
		s.mu.Unlock()
		return fmt.Errorf("代理配置不存在")
	}
	profile := updated.Profiles[index].Clone()
	updated.Profiles = append(updated.Profiles[:index], updated.Profiles[index+1:]...)
	s.mu.Unlock()
	if err := s.store.Save(updated); err != nil {
		return err
	}

	s.mu.Lock()
	s.cfg = updated
	server := s.instances[id]
	delete(s.instances, id)
	delete(s.starting, id)
	delete(s.errors, id)
	delete(s.connections, id)
	delete(s.revisions, id)
	s.mu.Unlock()
	if server != nil {
		server.Stop()
	}
	for _, ref := range secretRefs(profile) {
		_ = s.secrets.Delete(ref)
	}
	if profile.SSH != nil {
		_ = s.store.DeleteManagedPrivateKey(profile.SSH.PrivateKeyPath)
	}
	return nil
}

func (s *Service) Start(id string) error {
	op := s.profileLock(id)
	op.Lock()
	defer op.Unlock()

	s.mu.Lock()
	if running := s.instances[id]; running != nil && running.Running() {
		s.mu.Unlock()
		return nil
	}
	index := profileIndex(s.cfg, id)
	if index < 0 {
		s.mu.Unlock()
		return fmt.Errorf("代理配置不存在")
	}
	profile := s.cfg.Profiles[index].Clone()
	s.revisions[id]++
	revision := s.revisions[id]
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	s.starting[id] = cancel
	s.errors[id] = ""
	s.mu.Unlock()

	outbound, err := s.buildTransport(profile)
	var server *proxy.Server
	if err == nil {
		server = proxy.NewServer(profile.ListenAddress(), outbound, func(_ string, dialErr error) {
			s.recordConnectionResult(id, revision, profile, dialErr)
		})
		err = server.Start(ctx)
	}
	canceled := ctx.Err() != nil
	cancel()

	s.mu.Lock()
	delete(s.starting, id)
	if err != nil {
		s.errors[id] = err.Error()
		s.mu.Unlock()
		return err
	}
	if canceled {
		s.errors[id] = ""
		s.mu.Unlock()
		server.Stop()
		return context.Canceled
	}
	s.instances[id] = server
	s.errors[id] = ""
	s.mu.Unlock()
	return nil
}

func (s *Service) Stop(id string) {
	s.cancelStart(id)
	op := s.profileLock(id)
	op.Lock()
	defer op.Unlock()
	s.mu.Lock()
	server := s.instances[id]
	delete(s.instances, id)
	delete(s.starting, id)
	s.revisions[id]++
	s.errors[id] = ""
	s.mu.Unlock()
	if server != nil {
		server.Stop()
	}
}

func (s *Service) TestConnection(id string) error {
	op := s.profileLock(id)
	op.Lock()
	defer op.Unlock()

	s.mu.Lock()
	index := profileIndex(s.cfg, id)
	if index < 0 {
		s.mu.Unlock()
		return fmt.Errorf("代理配置不存在")
	}
	profile := s.cfg.Profiles[index].Clone()
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	outbound, err := s.buildTransport(profile)
	if err == nil {
		err = outbound.Start(ctx)
	}
	if err == nil && profile.Type == model.ProxyTypeWebSocket {
		err = testWebSocketConnection(ctx, profile, outbound)
	}
	if outbound != nil {
		_ = outbound.Close()
	}

	result := newConnectionHealth(profile, err)
	s.mu.Lock()
	s.connections[id] = result
	s.mu.Unlock()
	if err == nil {
		return nil
	}
	log.Printf("[Easy-Net Lite] 配置 %q 测试连接失败：%s", profile.Name, result.Error)
	if profile.Type == model.ProxyTypeWebSocket {
		return errors.New(result.Error)
	}
	return err
}

func (s *Service) recordConnectionResult(id string, revision uint64, profile model.Profile, err error) {
	result := newConnectionHealth(profile, err)
	s.mu.Lock()
	if s.revisions[id] != revision {
		s.mu.Unlock()
		return
	}
	previous := s.connections[id]
	s.connections[id] = result
	shouldLog := err != nil && (previous.Error != result.Error || result.CheckedAt.Sub(previous.CheckedAt) >= 30*time.Second)
	s.mu.Unlock()
	if shouldLog {
		log.Printf("[Easy-Net Lite] 配置 %q 远端连接失败：%s", profile.Name, result.Error)
	}
}

func newConnectionHealth(profile model.Profile, err error) connectionHealth {
	result := connectionHealth{Status: "success", CheckedAt: time.Now().UTC()}
	if err != nil {
		result.Status = "error"
		result.Error = friendlyConnectionError(profile, err)
	}
	return result
}

func friendlyConnectionError(profile model.Profile, err error) string {
	message := err.Error()
	if profile.Type != model.ProxyTypeWebSocket {
		return message
	}
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "http 401"), strings.Contains(lower, "http 403"):
		return "WebSocket 认证失败，请检查连接密钥"
	case strings.Contains(lower, "http 404"):
		return "WebSocket 服务端拒绝连接（HTTP 404），请检查地址、密钥以及“兼容旧服务端”设置"
	case strings.Contains(lower, "x509"), strings.Contains(lower, "certificate"):
		return "WebSocket TLS 证书校验失败，请检查域名和证书配置"
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline exceeded"):
		return "连接 WebSocket 服务端超时，请检查网络或服务端状态"
	case strings.Contains(lower, "no such host"):
		return "无法解析 WebSocket 服务端域名，请检查地址或 DNS"
	case strings.Contains(lower, "connection refused"), strings.Contains(lower, "actively refused"), strings.Contains(lower, "connectex"):
		return "WebSocket 服务端拒绝连接，请检查服务是否启动及端口是否正确"
	default:
		return "WebSocket 连接失败：" + message
	}
}

func testWebSocketConnection(ctx context.Context, profile model.Profile, outbound transport.Transport) error {
	target, err := websocketProbeTarget(profile.WebSocket.URL)
	if err != nil {
		return err
	}
	conn, err := outbound.DialContext(ctx, "tcp", target)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(900 * time.Millisecond)); err != nil {
		return err
	}
	probe := make([]byte, 1)
	_, err = conn.Read(probe)
	if err == nil {
		return nil
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return nil
	}
	return fmt.Errorf("连接建立后被服务端关闭：%w", err)
}

func websocketProbeTarget(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if !strings.Contains(rawURL, "://") {
		rawURL = "wss://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("WebSocket 地址无效")
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "ws" || u.Scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port), nil
}

func (s *Service) StartAuto() {
	for _, state := range s.States() {
		if state.Profile.AutoStart {
			_ = s.Start(state.Profile.ID)
		}
	}
}

func (s *Service) StartAll() {
	for _, state := range s.States() {
		_ = s.Start(state.Profile.ID)
	}
}

func (s *Service) StopAll() {
	states := s.States()
	for _, state := range states {
		s.Stop(state.Profile.ID)
	}
}

func (s *Service) TrustSSHHost(id, fingerprint string) error {
	s.cancelStart(id)
	op := s.profileLock(id)
	op.Lock()
	defer op.Unlock()
	s.configMu.Lock()
	defer s.configMu.Unlock()

	s.mu.Lock()
	updated := cloneConfig(s.cfg)
	index := profileIndex(updated, id)
	if index < 0 || updated.Profiles[index].SSH == nil {
		s.mu.Unlock()
		return fmt.Errorf("SSH 配置不存在")
	}
	updated.Profiles[index].SSH.HostKeyFingerprint = fingerprint
	s.mu.Unlock()
	if err := s.store.Save(updated); err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg = updated
	s.mu.Unlock()
	return nil
}

func (s *Service) buildTransport(profile model.Profile) (transport.Transport, error) {
	switch profile.Type {
	case model.ProxyTypeWebSocket:
		secret, err := s.getSecret(profile.WebSocket.SecretRef, "WebSocket 密钥")
		if err != nil {
			return nil, err
		}
		return websockettransport.New(websockettransport.Config{
			URL: profile.WebSocket.URL, Secret: secret, AllowInsecure: profile.WebSocket.AllowInsecure,
			LegacyQueryAuth: profile.WebSocket.LegacyQueryAuth,
		})
	case model.ProxyTypeSSH:
		cfg := sshtransport.Config{
			Address: net.JoinHostPort(profile.SSH.Host, strconv.Itoa(profile.SSH.Port)), Username: profile.SSH.Username,
			PrivateKeyPath: profile.SSH.PrivateKeyPath, HostKeyFingerprint: profile.SSH.HostKeyFingerprint,
		}
		var err error
		if profile.SSH.AuthType == model.AuthTypePassword {
			cfg.Password, err = s.getSecret(profile.SSH.PasswordRef, "SSH 密码")
		} else if profile.SSH.PassphraseRef != "" {
			cfg.PrivateKeyPassphrase, err = s.getOptionalSecret(profile.SSH.PassphraseRef, "私钥口令")
		}
		if err != nil {
			return nil, err
		}
		return sshtransport.New(cfg), nil
	default:
		return nil, fmt.Errorf("不支持的代理类型")
	}
}

func (s *Service) getSecret(ref, label string) (string, error) {
	value, err := s.secrets.Get(ref)
	if err != nil {
		return "", fmt.Errorf("从系统凭据库读取%s失败：%w", label, err)
	}
	return value, nil
}

func (s *Service) getOptionalSecret(ref, label string) (string, error) {
	value, err := s.secrets.Get(ref)
	if secretstore.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("从系统凭据库读取%s失败：%w", label, err)
	}
	return value, nil
}

func (s *Service) requireSecret(ref, label string) error {
	value, err := s.secrets.Get(ref)
	if err != nil || value == "" {
		if err == nil {
			err = errors.New("内容为空")
		}
		return fmt.Errorf("%s未设置或无法从系统凭据库读取：%w", label, err)
	}
	return nil
}

type secretRollback struct {
	ref     string
	old     string
	existed bool
}

func (s *Service) saveSecretsTransactional(profile model.Profile, values SecretValues) (func(), error) {
	pairs := make([]struct{ ref, value string }, 0, 3)
	if profile.WebSocket != nil && values.WebSocketSecret != "" {
		pairs = append(pairs, struct{ ref, value string }{profile.WebSocket.SecretRef, values.WebSocketSecret})
	}
	if profile.SSH != nil && profile.SSH.AuthType == model.AuthTypePassword && values.SSHPassword != "" {
		pairs = append(pairs, struct{ ref, value string }{profile.SSH.PasswordRef, values.SSHPassword})
	}
	if profile.SSH != nil && profile.SSH.AuthType == model.AuthTypePrivateKey && values.SSHPassphrase != "" {
		pairs = append(pairs, struct{ ref, value string }{profile.SSH.PassphraseRef, values.SSHPassphrase})
	}
	changes := make([]secretRollback, 0, len(pairs))
	rollback := func() {
		for i := len(changes) - 1; i >= 0; i-- {
			change := changes[i]
			if change.existed {
				_ = s.secrets.Set(change.ref, change.old)
			} else {
				_ = s.secrets.Delete(change.ref)
			}
		}
	}
	for _, pair := range pairs {
		old, err := s.secrets.Get(pair.ref)
		existed := err == nil
		if err != nil && !secretstore.IsNotFound(err) {
			rollback()
			return func() {}, fmt.Errorf("读取系统凭据库失败：%w", err)
		}
		if err := s.secrets.Set(pair.ref, pair.value); err != nil {
			rollback()
			return func() {}, fmt.Errorf("写入系统凭据库失败：%w", err)
		}
		changes = append(changes, secretRollback{ref: pair.ref, old: old, existed: existed})
	}
	return rollback, nil
}

func (s *Service) cleanupReplacedCredentials(old, current model.Profile) {
	activeRefs := make(map[string]struct{})
	for _, ref := range secretRefs(current) {
		activeRefs[ref] = struct{}{}
	}
	for _, ref := range secretRefs(old) {
		if _, active := activeRefs[ref]; !active {
			_ = s.secrets.Delete(ref)
		}
	}
	oldKey := ""
	currentKey := ""
	if old.SSH != nil {
		oldKey = old.SSH.PrivateKeyPath
	}
	if current.SSH != nil {
		currentKey = current.SSH.PrivateKeyPath
	}
	if oldKey != "" && oldKey != currentKey {
		_ = s.store.DeleteManagedPrivateKey(oldKey)
	}
}

func secretRefs(profile model.Profile) []string {
	refs := []string{}
	if profile.WebSocket != nil && profile.WebSocket.SecretRef != "" {
		refs = append(refs, profile.WebSocket.SecretRef)
	}
	if profile.SSH != nil {
		if profile.SSH.PasswordRef != "" {
			refs = append(refs, profile.SSH.PasswordRef)
		}
		if profile.SSH.PassphraseRef != "" {
			refs = append(refs, profile.SSH.PassphraseRef)
		}
	}
	return refs
}

func cloneConfig(cfg *model.Config) *model.Config {
	result := &model.Config{Version: cfg.Version, Profiles: make([]model.Profile, len(cfg.Profiles))}
	for i := range cfg.Profiles {
		result.Profiles[i] = cfg.Profiles[i].Clone()
	}
	return result
}

func profileIndex(cfg *model.Config, id string) int {
	for i := range cfg.Profiles {
		if cfg.Profiles[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *Service) profileLock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.profileLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.profileLocks[id] = lock
	}
	return lock
}

func (s *Service) cancelStart(id string) {
	s.mu.Lock()
	cancel := s.starting[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func newID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func AsUnknownHostKey(err error) (*sshtransport.HostKeyUnknownError, bool) {
	var target *sshtransport.HostKeyUnknownError
	ok := errors.As(err, &target)
	return target, ok
}
