package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"easy-net/client-lite/internal/config"
	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/proxy"
	"easy-net/client-lite/internal/secretstore"
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
	Profile model.Profile `json:"profile"`
	Running bool          `json:"running"`
	Error   string        `json:"error,omitempty"`
}

type Service struct {
	mu        sync.Mutex
	store     *config.Store
	secrets   secretstore.Store
	cfg       *model.Config
	instances map[string]*proxy.Server
	errors    map[string]string
}

func New(store *config.Store, secrets secretstore.Store) (*Service, error) {
	cfg, err := store.Load()
	if err != nil {
		return nil, err
	}
	return &Service{
		store: store, secrets: secrets, cfg: cfg,
		instances: make(map[string]*proxy.Server), errors: make(map[string]string),
	}, nil
}

func (s *Service) ConfigPath() string { return s.store.Path() }

func (s *Service) States() []ProfileState {
	s.mu.Lock()
	defer s.mu.Unlock()
	states := make([]ProfileState, 0, len(s.cfg.Profiles))
	for _, p := range s.cfg.Profiles {
		server := s.instances[p.ID]
		states = append(states, ProfileState{Profile: p.Clone(), Running: server != nil && server.Running(), Error: s.errors[p.ID]})
	}
	sort.SliceStable(states, func(i, j int) bool { return states[i].Profile.Name < states[j].Profile.Name })
	return states
}

func (s *Service) Profile(id string) (model.Profile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.cfg.Profiles {
		if p.ID == id {
			return p.Clone(), true
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

func (s *Service) Upsert(profile model.Profile, values SecretValues) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if profile.ID == "" {
		profile.ID = newID()
	}
	profile.Normalize()
	oldIndex := -1
	wasRunning := false
	oldPrivateKeyPath := ""
	for i, existing := range s.cfg.Profiles {
		if existing.ID == profile.ID {
			oldIndex = i
			wasRunning = s.instances[profile.ID] != nil && s.instances[profile.ID].Running()
			preserveSecretRefs(&profile, existing)
			if existing.SSH != nil {
				oldPrivateKeyPath = existing.SSH.PrivateKeyPath
			}
			break
		}
	}
	newPrivateKeyPath := ""
	if profile.SSH != nil && profile.SSH.AuthType == model.AuthTypePrivateKey && len(values.SSHPrivateKey) > 0 {
		path, err := s.store.SavePrivateKey(values.SSHPrivateKey)
		if err != nil {
			return err
		}
		newPrivateKeyPath = path
		profile.SSH.PrivateKeyPath = path
	}
	if profile.SSH != nil && profile.SSH.AuthType != model.AuthTypePrivateKey {
		profile.SSH.PrivateKeyPath = ""
	}
	cleanupNewKey := func() {
		if newPrivateKeyPath != "" {
			_ = s.store.DeleteManagedPrivateKey(newPrivateKeyPath)
		}
	}
	assignSecretRefs(&profile)
	if err := profile.Validate(); err != nil {
		cleanupNewKey()
		return err
	}
	for _, existing := range s.cfg.Profiles {
		if existing.ID != profile.ID && existing.ListenHost == profile.ListenHost && existing.ListenPort == profile.ListenPort {
			cleanupNewKey()
			return fmt.Errorf("本地端口 %d 已被配置 %q 使用", profile.ListenPort, existing.Name)
		}
	}
	if err := s.saveSecrets(profile, values); err != nil {
		cleanupNewKey()
		return err
	}
	if profile.WebSocket != nil {
		if err := s.requireSecret(profile.WebSocket.SecretRef, "WebSocket 密钥"); err != nil {
			cleanupNewKey()
			return err
		}
	}
	if profile.SSH != nil && profile.SSH.AuthType == model.AuthTypePassword {
		if err := s.requireSecret(profile.SSH.PasswordRef, "SSH 密码"); err != nil {
			cleanupNewKey()
			return err
		}
	}
	if running := s.instances[profile.ID]; running != nil {
		running.Stop()
		delete(s.instances, profile.ID)
	}
	updated := &model.Config{Profiles: make([]model.Profile, len(s.cfg.Profiles))}
	for i, existing := range s.cfg.Profiles {
		updated.Profiles[i] = existing.Clone()
	}
	if oldIndex >= 0 {
		updated.Profiles[oldIndex] = profile
	} else {
		updated.Profiles = append(updated.Profiles, profile)
	}
	if err := s.store.Save(updated); err != nil {
		cleanupNewKey()
		return err
	}
	s.cfg = updated
	currentPrivateKeyPath := ""
	if profile.SSH != nil {
		currentPrivateKeyPath = profile.SSH.PrivateKeyPath
	}
	if oldPrivateKeyPath != "" && oldPrivateKeyPath != currentPrivateKeyPath {
		_ = s.store.DeleteManagedPrivateKey(oldPrivateKeyPath)
	}
	s.errors[profile.ID] = ""
	if wasRunning {
		if err := s.startLocked(profile.ID); err != nil {
			s.errors[profile.ID] = err.Error()
			return fmt.Errorf("配置已保存，但重新启动失败：%w", err)
		}
	}
	return nil
}

func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	var profile model.Profile
	for i, p := range s.cfg.Profiles {
		if p.ID == id {
			index, profile = i, p
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("代理配置不存在")
	}
	if running := s.instances[id]; running != nil {
		running.Stop()
		delete(s.instances, id)
	}
	s.cfg.Profiles = append(s.cfg.Profiles[:index], s.cfg.Profiles[index+1:]...)
	if err := s.store.Save(s.cfg); err != nil {
		return err
	}
	for _, ref := range secretRefs(profile) {
		_ = s.secrets.Delete(ref)
	}
	if profile.SSH != nil {
		_ = s.store.DeleteManagedPrivateKey(profile.SSH.PrivateKeyPath)
	}
	delete(s.errors, id)
	return nil
}

func (s *Service) Start(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.startLocked(id)
	if err != nil {
		s.errors[id] = err.Error()
	} else {
		s.errors[id] = ""
	}
	return err
}

func (s *Service) startLocked(id string) error {
	if running := s.instances[id]; running != nil && running.Running() {
		return nil
	}
	var profile *model.Profile
	for i := range s.cfg.Profiles {
		if s.cfg.Profiles[i].ID == id {
			profile = &s.cfg.Profiles[i]
			break
		}
	}
	if profile == nil {
		return fmt.Errorf("代理配置不存在")
	}
	outbound, err := s.buildTransport(*profile)
	if err != nil {
		return err
	}
	server := proxy.NewServer(profile.ListenAddress(), outbound)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		return err
	}
	s.instances[id] = server
	return nil
}

func (s *Service) Stop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if running := s.instances[id]; running != nil {
		running.Stop()
		delete(s.instances, id)
	}
	s.errors[id] = ""
}

func (s *Service) StartAuto() {
	states := s.States()
	for _, state := range states {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, running := range s.instances {
		running.Stop()
		delete(s.instances, id)
	}
}

func (s *Service) TrustSSHHost(id, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.cfg.Profiles {
		if s.cfg.Profiles[i].ID == id && s.cfg.Profiles[i].SSH != nil {
			s.cfg.Profiles[i].SSH.HostKeyFingerprint = fingerprint
			return s.store.Save(s.cfg)
		}
	}
	return fmt.Errorf("SSH 配置不存在")
}

func (s *Service) buildTransport(profile model.Profile) (transport.Transport, error) {
	switch profile.Type {
	case model.ProxyTypeWebSocket:
		secret, err := s.getSecret(profile.WebSocket.SecretRef, "WebSocket 密钥")
		if err != nil {
			return nil, err
		}
		return websockettransport.New(profile.WebSocket.URL, secret)
	case model.ProxyTypeSSH:
		cfg := sshtransport.Config{
			Address:            net.JoinHostPort(profile.SSH.Host, strconv.Itoa(profile.SSH.Port)),
			Username:           profile.SSH.Username,
			PrivateKeyPath:     profile.SSH.PrivateKeyPath,
			HostKeyFingerprint: profile.SSH.HostKeyFingerprint,
		}
		var err error
		if profile.SSH.AuthType == model.AuthTypePassword {
			cfg.Password, err = s.getSecret(profile.SSH.PasswordRef, "SSH 密码")
		} else if profile.SSH.PassphraseRef != "" {
			cfg.PrivateKeyPassphrase, err = s.getSecret(profile.SSH.PassphraseRef, "私钥口令")
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

func (s *Service) saveSecrets(profile model.Profile, values SecretValues) error {
	pairs := map[string]string{}
	if profile.WebSocket != nil && values.WebSocketSecret != "" {
		pairs[profile.WebSocket.SecretRef] = values.WebSocketSecret
	}
	if profile.SSH != nil && values.SSHPassword != "" {
		pairs[profile.SSH.PasswordRef] = values.SSHPassword
	}
	if profile.SSH != nil && values.SSHPassphrase != "" {
		pairs[profile.SSH.PassphraseRef] = values.SSHPassphrase
	}
	for ref, value := range pairs {
		if err := s.secrets.Set(ref, value); err != nil {
			return fmt.Errorf("写入系统凭据库失败：%w", err)
		}
	}
	return nil
}

func assignSecretRefs(profile *model.Profile) {
	if profile.WebSocket != nil && profile.WebSocket.SecretRef == "" {
		profile.WebSocket.SecretRef = profile.ID + "/websocket"
	}
	if profile.SSH != nil {
		if profile.SSH.AuthType == model.AuthTypePassword && profile.SSH.PasswordRef == "" {
			profile.SSH.PasswordRef = profile.ID + "/password"
		}
		if profile.SSH.AuthType == model.AuthTypePrivateKey && profile.SSH.PassphraseRef == "" {
			profile.SSH.PassphraseRef = profile.ID + "/passphrase"
		}
	}
}

func preserveSecretRefs(profile *model.Profile, old model.Profile) {
	if profile.WebSocket != nil && old.WebSocket != nil {
		profile.WebSocket.SecretRef = old.WebSocket.SecretRef
	}
	if profile.SSH != nil && old.SSH != nil {
		profile.SSH.PasswordRef = old.SSH.PasswordRef
		profile.SSH.PassphraseRef = old.SSH.PassphraseRef
		if profile.SSH.HostKeyFingerprint == "" && profile.SSH.Host == old.SSH.Host && profile.SSH.Port == old.SSH.Port {
			profile.SSH.HostKeyFingerprint = old.SSH.HostKeyFingerprint
		}
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
