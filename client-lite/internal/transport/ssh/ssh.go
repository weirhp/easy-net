package ssh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

type Config struct {
	Address              string
	Username             string
	Password             string
	PrivateKeyPath       string
	PrivateKeyPassphrase string
	HostKeyFingerprint   string
}

type HostKeyUnknownError struct {
	Address     string
	Fingerprint string
}

func (e *HostKeyUnknownError) Error() string {
	return fmt.Sprintf("首次连接 SSH 服务器 %s，请确认指纹 %s", e.Address, e.Fingerprint)
}

type HostKeyMismatchError struct {
	Expected string
	Actual   string
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("SSH 服务器指纹不匹配：已保存 %s，实际 %s", e.Expected, e.Actual)
}

type Transport struct {
	cfg           Config
	mu            sync.Mutex
	client        *gossh.Client
	closed        bool
	dialing       bool
	dialDone      chan struct{}
	stop          chan struct{}
	closeOnce     sync.Once
	keepAliveOnce sync.Once
}

func New(cfg Config) *Transport {
	return &Transport{cfg: cfg, stop: make(chan struct{})}
}

func (t *Transport) Start(ctx context.Context) error {
	_, err := t.ensureClient(ctx)
	if err == nil {
		t.keepAliveOnce.Do(func() { go t.keepAlive() })
	}
	return err
}

func (t *Transport) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" {
		return nil, fmt.Errorf("SSH 代理仅支持 TCP")
	}
	client, err := t.ensureClient(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := client.DialContext(ctx, network, address)
	if err == nil {
		return conn, nil
	}
	t.invalidate(client)
	client, reconnectErr := t.ensureClient(ctx)
	if reconnectErr != nil {
		return nil, fmt.Errorf("SSH 重连失败：%w", reconnectErr)
	}
	conn, err = client.DialContext(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("SSH 转发到 %s：%w", address, err)
	}
	return conn, nil
}

func (t *Transport) Close() error {
	t.closeOnce.Do(func() { close(t.stop) })
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	if t.client != nil {
		err := t.client.Close()
		t.client = nil
		return err
	}
	return nil
}

func (t *Transport) ensureClient(ctx context.Context) (*gossh.Client, error) {
	for {
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			return nil, errors.New("SSH 代理已停止")
		}
		if t.client != nil {
			client := t.client
			t.mu.Unlock()
			return client, nil
		}
		if t.dialing {
			done := t.dialDone
			t.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		t.dialing = true
		t.dialDone = make(chan struct{})
		done := t.dialDone
		t.mu.Unlock()

		client, err := t.dialClient(ctx)
		t.mu.Lock()
		if err == nil && !t.closed && t.client == nil {
			t.client = client
		} else if client != nil {
			_ = client.Close()
		}
		if t.closed && err == nil {
			err = errors.New("SSH 代理已停止")
		}
		t.dialing = false
		close(done)
		result := t.client
		t.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return result, nil
	}
}

func (t *Transport) dialClient(ctx context.Context) (*gossh.Client, error) {
	clientConfig, err := t.clientConfig()
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", t.cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("连接 SSH 服务器：%w", err)
	}
	conn, chans, reqs, err := gossh.NewClientConn(raw, t.cfg.Address, clientConfig)
	if err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("SSH 握手或认证失败：%w", err)
	}
	return gossh.NewClient(conn, chans, reqs), nil
}

func (t *Transport) clientConfig() (*gossh.ClientConfig, error) {
	var auth gossh.AuthMethod
	if t.cfg.PrivateKeyPath != "" {
		data, err := os.ReadFile(t.cfg.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("读取 SSH 私钥：%w", err)
		}
		var signer gossh.Signer
		if t.cfg.PrivateKeyPassphrase != "" {
			signer, err = gossh.ParsePrivateKeyWithPassphrase(data, []byte(t.cfg.PrivateKeyPassphrase))
		} else {
			signer, err = gossh.ParsePrivateKey(data)
		}
		if err != nil {
			return nil, fmt.Errorf("解析 SSH 私钥：%w", err)
		}
		auth = gossh.PublicKeys(signer)
	} else {
		auth = gossh.Password(t.cfg.Password)
	}
	expected := t.cfg.HostKeyFingerprint
	return &gossh.ClientConfig{
		User:    t.cfg.Username,
		Auth:    []gossh.AuthMethod{auth},
		Timeout: 10 * time.Second,
		HostKeyCallback: func(hostname string, remote net.Addr, key gossh.PublicKey) error {
			actual := gossh.FingerprintSHA256(key)
			if expected == "" {
				return &HostKeyUnknownError{Address: hostname, Fingerprint: actual}
			}
			if actual != expected {
				return &HostKeyMismatchError{Expected: expected, Actual: actual}
			}
			return nil
		},
	}, nil
}

func (t *Transport) invalidate(client *gossh.Client) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client == client {
		_ = t.client.Close()
		t.client = nil
	}
}

func (t *Transport) keepAlive() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-ticker.C:
			t.mu.Lock()
			client := t.client
			t.mu.Unlock()
			if client == nil {
				continue
			}
			if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				t.invalidate(client)
			}
		}
	}
}
