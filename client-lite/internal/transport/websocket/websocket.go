package websocket

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Transport struct {
	url    *url.URL
	secret string
}

func New(rawURL, secret string) (*Transport, error) {
	rawURL = strings.TrimSpace(rawURL)
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
	if u.Host == "" {
		return nil, fmt.Errorf("WebSocket 地址缺少服务器")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/tunnel"
	}
	return &Transport{url: u, secret: secret}, nil
}

func (t *Transport) Start(context.Context) error { return nil }

func (t *Transport) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" {
		return nil, fmt.Errorf("WebSocket 代理仅支持 TCP")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("解析目标地址：%w", err)
	}
	u := *t.url
	q := u.Query()
	q.Set("secret", t.secret) // 兼容当前 Easy-Net 服务端协议。
	q.Set("host", host)
	q.Set("port", port)
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig:  &tls.Config{MinVersion: tls.VersionTLS12},
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+t.secret)
	headers.Set("User-Agent", "Easy-Net-Lite/1.0")
	c, resp, err := dialer.DialContext(ctx, u.String(), headers)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("WebSocket 握手失败（HTTP %d）：%w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("连接 WebSocket：%w", err)
	}
	return &streamConn{conn: c, remote: wsAddr(u.Host)}, nil
}

func (t *Transport) Close() error { return nil }

type streamConn struct {
	conn   *websocket.Conn
	reader io.Reader
	write  sync.Mutex
	remote net.Addr
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
			return n, err
		}
		kind, r, err := c.conn.NextReader()
		if err != nil {
			return 0, err
		}
		if kind == websocket.BinaryMessage || kind == websocket.TextMessage {
			c.reader = r
		}
	}
}

func (c *streamConn) Write(p []byte) (int, error) {
	c.write.Lock()
	defer c.write.Unlock()
	if err := c.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *streamConn) Close() error         { return c.conn.Close() }
func (c *streamConn) LocalAddr() net.Addr  { return wsAddr("127.0.0.1:0") }
func (c *streamConn) RemoteAddr() net.Addr { return c.remote }
func (c *streamConn) SetDeadline(t time.Time) error {
	_ = c.conn.SetReadDeadline(t)
	return c.conn.SetWriteDeadline(t)
}
func (c *streamConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *streamConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }

type wsAddr string

func (a wsAddr) Network() string { return "websocket" }
func (a wsAddr) String() string  { return string(a) }
