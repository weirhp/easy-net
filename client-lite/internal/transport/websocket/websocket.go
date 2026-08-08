package websocket

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"easy-net/client-lite/internal/version"

	"github.com/gorilla/websocket"
)

const (
	heartbeatInterval    = 25 * time.Second
	heartbeatTimeout     = 60 * time.Second
	tunnelProtocolHeader = "X-Easy-Net-Protocol"
	tunnelProtocolV2     = "2"
	tunnelReadyMessage   = "READY"
	tunnelErrorPrefix    = "ERROR "
)

type Config struct {
	URL             string
	Secret          string
	AllowInsecure   bool
	LegacyQueryAuth bool
}

type Transport struct {
	url             *url.URL
	secret          string
	legacyQueryAuth bool
	mu              sync.Mutex
	connections     map[*streamConn]struct{}
	closed          bool
}

func New(cfg Config) (*Transport, error) {
	rawURL := strings.TrimSpace(cfg.URL)
	if !strings.Contains(rawURL, "://") {
		rawURL = "wss://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("无效的 WebSocket 地址：%w", err)
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return nil, fmt.Errorf("WebSocket 地址必须使用 ws:// 或 wss://")
	}
	if u.Scheme == "ws" && !cfg.AllowInsecure {
		return nil, fmt.Errorf("未加密的 ws:// 连接必须明确允许")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("WebSocket 地址缺少服务器")
	}
	if u.User != nil {
		return nil, fmt.Errorf("WebSocket 地址不能包含用户名或密码")
	}
	if u.Fragment != "" {
		return nil, fmt.Errorf("WebSocket 地址不能包含片段")
	}
	if u.Query().Has("secret") {
		return nil, fmt.Errorf("WebSocket 地址不能包含 secret 参数，请单独配置密钥")
	}
	query := u.Query()
	query.Del("host")
	query.Del("port")
	u.RawQuery = query.Encode()
	if u.Path == "" || u.Path == "/" {
		u.Path = "/tunnel"
	}
	return &Transport{
		url: u, secret: cfg.Secret, legacyQueryAuth: cfg.LegacyQueryAuth,
		connections: make(map[*streamConn]struct{}),
	}, nil
}

func (t *Transport) Start(context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("WebSocket 代理已停止")
	}
	return nil
}

func (t *Transport) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" {
		return nil, fmt.Errorf("WebSocket 代理仅支持 TCP")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("解析目标地址：%w", err)
	}
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return nil, errors.New("WebSocket 代理已停止")
	}

	u := *t.url
	if t.legacyQueryAuth {
		q := u.Query()
		q.Set("secret", t.secret)
		q.Set("host", host)
		q.Set("port", port)
		u.RawQuery = q.Encode()
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig:  &tls.Config{MinVersion: tls.VersionTLS12},
		Proxy:            http.ProxyFromEnvironment,
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+t.secret)
	headers.Set("X-Target-Host", host)
	headers.Set("X-Target-Port", port)
	headers.Set(tunnelProtocolHeader, tunnelProtocolV2)
	headers.Set("User-Agent", "Easy-Net-Lite/"+version.Value)
	conn, resp, err := dialer.DialContext(ctx, u.String(), headers)
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("WebSocket 握手失败（HTTP %d）：%w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("连接 WebSocket：%w", err)
	}
	if resp != nil && resp.Header.Get(tunnelProtocolHeader) == tunnelProtocolV2 {
		deadline := time.Now().Add(10 * time.Second)
		if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
			deadline = contextDeadline
		}
		_ = conn.SetReadDeadline(deadline)
		kind, payload, readyErr := conn.ReadMessage()
		_ = conn.SetReadDeadline(time.Time{})
		if readyErr != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("等待远端目标连接就绪：%w", readyErr)
		}
		message := string(payload)
		if kind != websocket.TextMessage || message != tunnelReadyMessage {
			_ = conn.Close()
			if strings.HasPrefix(message, tunnelErrorPrefix) {
				return nil, fmt.Errorf("远端目标连接失败：%s", strings.TrimSpace(strings.TrimPrefix(message, tunnelErrorPrefix)))
			}
			return nil, errors.New("WebSocket 服务端返回了无效的目标连接状态")
		}
	}
	stream := &streamConn{conn: conn, remote: wsAddr(u.Host), heartbeatDone: make(chan struct{})}
	stream.lastPong.Store(time.Now().UnixNano())
	stream.onClose = func() {
		t.mu.Lock()
		delete(t.connections, stream)
		t.mu.Unlock()
	}
	conn.SetPongHandler(func(string) error {
		stream.lastPong.Store(time.Now().UnixNano())
		return nil
	})
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = stream.Close()
		return nil, errors.New("WebSocket 代理已停止")
	}
	t.connections[stream] = struct{}{}
	t.mu.Unlock()
	go stream.heartbeat()
	return stream, nil
}

func (t *Transport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	connections := make([]*streamConn, 0, len(t.connections))
	for conn := range t.connections {
		connections = append(connections, conn)
	}
	t.mu.Unlock()
	var firstErr error
	for _, conn := range connections {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type streamConn struct {
	conn          *websocket.Conn
	reader        io.Reader
	write         sync.Mutex
	remote        net.Addr
	closeOnce     sync.Once
	heartbeatDone chan struct{}
	lastPong      atomic.Int64
	onClose       func()
}

func (c *streamConn) Read(p []byte) (int, error) {
	for {
		if c.reader != nil {
			n, err := c.reader.Read(p)
			if err == io.EOF {
				c.reader = nil
				if n > 0 {
					return n, nil
				}
				continue
			}
			if err != nil {
				c.close(false)
			}
			return n, err
		}
		kind, reader, err := c.conn.NextReader()
		if err != nil {
			c.close(false)
			return 0, err
		}
		if kind == websocket.BinaryMessage || kind == websocket.TextMessage {
			c.reader = reader
		}
	}
}

func (c *streamConn) Write(p []byte) (int, error) {
	c.write.Lock()
	defer c.write.Unlock()
	if err := c.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		c.close(false)
		return 0, err
	}
	return len(p), nil
}

func (c *streamConn) heartbeat() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.heartbeatDone:
			return
		case now := <-ticker.C:
			lastPong := time.Unix(0, c.lastPong.Load())
			if now.Sub(lastPong) > heartbeatTimeout {
				c.close(false)
				return
			}
			c.write.Lock()
			err := c.conn.WriteControl(websocket.PingMessage, nil, now.Add(5*time.Second))
			c.write.Unlock()
			if err != nil {
				c.close(false)
				return
			}
		}
	}
}

func (c *streamConn) close(sendControl bool) error {
	var closeErr error
	c.closeOnce.Do(func() {
		close(c.heartbeatDone)
		if sendControl {
			c.write.Lock()
			_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			c.write.Unlock()
		}
		closeErr = c.conn.Close()
		if c.onClose != nil {
			c.onClose()
		}
	})
	return closeErr
}

func (c *streamConn) Close() error         { return c.close(true) }
func (c *streamConn) LocalAddr() net.Addr  { return wsAddr("127.0.0.1:0") }
func (c *streamConn) RemoteAddr() net.Addr { return c.remote }
func (c *streamConn) SetDeadline(deadline time.Time) error {
	_ = c.conn.SetReadDeadline(deadline)
	return c.conn.SetWriteDeadline(deadline)
}
func (c *streamConn) SetReadDeadline(deadline time.Time) error {
	return c.conn.SetReadDeadline(deadline)
}
func (c *streamConn) SetWriteDeadline(deadline time.Time) error {
	return c.conn.SetWriteDeadline(deadline)
}

type wsAddr string

func (a wsAddr) Network() string { return "websocket" }
func (a wsAddr) String() string  { return string(a) }
