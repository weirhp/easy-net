package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type SocksServer struct {
	cfg      EasyNetConfig
	listener net.Listener
	running  atomic.Bool
	stopOnce sync.Once
}

func NewSocksServer(cfg EasyNetConfig) *SocksServer {
	return &SocksServer{cfg: cfg}
}

func (s *SocksServer) Start() error {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.cfg.LocalPort))
	if err != nil {
		return fmt.Errorf("listen 127.0.0.1:%d: %w", s.cfg.LocalPort, err)
	}
	s.listener = listener
	s.running.Store(true)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				if s.running.Load() {
					log.Printf("[Easy-Net] accept error on %s: %v", s.cfg.ID, err)
				}
				return
			}
			go handleClient(conn, &s.cfg)
		}
	}()
	return nil
}

func (s *SocksServer) Stop() {
	s.stopOnce.Do(func() {
		s.running.Store(false)
		if s.listener != nil {
			_ = s.listener.Close()
		}
		log.Printf("[Easy-Net] socks server stopped: %s", s.cfg.ID)
	})
}

func (s *SocksServer) Running() bool {
	return s.running.Load()
}

func handleClient(conn net.Conn, config *EasyNetConfig) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(15 * time.Second))

	if err := socks5Handshake(conn); err != nil {
		log.Printf("[Easy-Net] handshake failed: %v", err)
		return
	}

	host, port, err := socks5ParseRequest(conn)
	if err != nil {
		log.Printf("[Easy-Net] parse request failed: %v", err)
		return
	}

	conn.SetDeadline(time.Time{})
	log.Printf("[Easy-Net] connecting to tunnel %s for target -> %s:%d", config.Name, host, port)
	wsConn, err := connectToWorker(host, port, config)
	if err != nil {
		log.Printf("[Easy-Net] tunnel connection failed to %s:%d: %v", host, port, err)
		_, _ = conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer wsConn.Close()

	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		log.Printf("[Easy-Net] failed to send CONNECT success response: %v", err)
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer wsConn.Close()
		buf := make([]byte, 32*1024)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				break
			}
			if n > 0 {
				if err := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
					log.Printf("[Easy-Net] ws write error: %v", err)
					break
				}
			}
		}
	}()

	go func() {
		defer wg.Done()
		defer conn.Close()
		for {
			msgType, p, err := wsConn.ReadMessage()
			if err != nil {
				break
			}
			if msgType == websocket.BinaryMessage || msgType == websocket.TextMessage {
				if _, err := conn.Write(p); err != nil {
					log.Printf("[Easy-Net] local write error: %v", err)
					break
				}
			}
		}
	}()

	wg.Wait()
	log.Printf("[Easy-Net] tunnel closed -> %s:%d", host, port)
}

func socks5Handshake(conn net.Conn) error {
	buf := make([]byte, 257)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return err
	}

	version := buf[0]
	nmethods := int(buf[1])
	if version != 5 {
		return fmt.Errorf("invalid SOCKS version: %d", version)
	}

	if _, err := io.ReadFull(conn, buf[:nmethods]); err != nil {
		return err
	}
	_, err := conn.Write([]byte{0x05, 0x00})
	return err
}

func socks5ParseRequest(conn net.Conn) (string, uint16, error) {
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "", 0, err
	}

	version := buf[0]
	cmd := buf[1]
	atyp := buf[3]

	if version != 5 {
		return "", 0, fmt.Errorf("invalid SOCKS version in request: %d", version)
	}
	if cmd != 1 {
		return "", 0, fmt.Errorf("unsupported command: %d", cmd)
	}

	var host string
	switch atyp {
	case 0x01:
		ipBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, ipBuf); err != nil {
			return "", 0, err
		}
		host = net.IP(ipBuf).String()
	case 0x03:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return "", 0, err
		}
		domainBuf := make([]byte, int(lenBuf[0]))
		if _, err := io.ReadFull(conn, domainBuf); err != nil {
			return "", 0, err
		}
		host = string(domainBuf)
	case 0x04:
		ipBuf := make([]byte, 16)
		if _, err := io.ReadFull(conn, ipBuf); err != nil {
			return "", 0, err
		}
		host = net.IP(ipBuf).String()
	default:
		return "", 0, fmt.Errorf("unsupported address type: %d", atyp)
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return "", 0, err
	}
	return host, binary.BigEndian.Uint16(portBuf), nil
}

func connectToWorker(host string, port uint16, config *EasyNetConfig) (*websocket.Conn, error) {
	resolvedHost := config.WorkerHost

	if config.EndpointIP != "" {
		resolvedHost = config.EndpointIP
		log.Printf("[Easy-Net] using configured endpoint IP: %s", resolvedHost)
	} else if !isIP(config.WorkerHost) {
		log.Printf("[Easy-Net] resolving relay host through 223.5.5.5: %s", config.WorkerHost)
		r := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(ctx, "udp", "223.5.5.5:53")
			},
		}
		ips, err := r.LookupHost(context.Background(), config.WorkerHost)
		if err == nil && len(ips) > 0 {
			resolvedHost = ips[0]
			log.Printf("[Easy-Net] resolved: %s -> %s", config.WorkerHost, resolvedHost)
		} else {
			log.Printf("[Easy-Net] direct DNS failed, fallback to system DNS: %v", err)
		}
	}

	u := url.URL{Scheme: "wss", Host: resolvedHost, Path: "/tunnel"}
	q := u.Query()
	q.Set("secret", config.Secret)
	q.Set("host", host)
	q.Set("port", strconv.Itoa(int(port)))
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			ServerName: config.WorkerHost,
		},
	}

	headers := make(http.Header)
	headers.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	headers.Set("Host", config.WorkerHost)

	wsConn, resp, err := dialer.Dial(u.String(), headers)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("status=%s code=%d body=%s err=%w", resp.Status, resp.StatusCode, string(body), err)
		}
		return nil, err
	}
	return wsConn, nil
}

func isIP(host string) bool {
	return net.ParseIP(host) != nil
}
