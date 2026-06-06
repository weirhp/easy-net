package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Config represents the client configuration
type Config struct {
	WorkerHost string `json:"workerHost"`
	LocalPort  int    `json:"localPort"`
	Secret     string `json:"secret"`
	EndpointIP string `json:"endpointIP"` // 新增优选IP字段
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// 1. Load config
	exePath, err := os.Executable()
	var dir string
	if err == nil {
		dir = filepath.Dir(exePath)
	} else {
		dir = "."
	}

	configPath := filepath.Join(dir, "local-config.json")
	// Fallback to current working directory if not found next to executable
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		configPath = "local-config.json"
	}

	log.Printf("[Easy-Net] Loading config from %s", configPath)
	configFile, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("[Easy-Net] Failed to read config file: %v", err)
	}

	var config Config
	if err := json.Unmarshal(configFile, &config); err != nil {
		log.Fatalf("[Easy-Net] Failed to parse config file: %v", err)
	}

	// 2. Start local SOCKS5 server
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", config.LocalPort))
	if err != nil {
		log.Fatalf("[Easy-Net] Failed to listen on 127.0.0.1:%d: %v", config.LocalPort, err)
	}
	defer listener.Close()

	log.Printf("=================================================")
	log.Printf("[Easy-Net] Go SOCKS5 加密代理客户端已启动！")
	log.Printf("监听地址: 127.0.0.1:%d", config.LocalPort)
	log.Printf("云端中继: wss://%s/tunnel", config.WorkerHost)
	log.Printf("=================================================")

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[Easy-Net] Accept error: %v", err)
			continue
		}
		go handleClient(conn, &config)
	}
}

func handleClient(conn net.Conn, config *Config) {
	defer conn.Close()

	// Set deadline for handshake
	conn.SetDeadline(time.Now().Add(15 * time.Second))

	// 1. SOCKS5 Handshake
	if err := socks5Handshake(conn); err != nil {
		log.Printf("[Easy-Net] Handshake failed: %v", err)
		return
	}

	// 2. Parse request
	host, port, err := socks5ParseRequest(conn)
	if err != nil {
		log.Printf("[Easy-Net] Parse request failed: %v", err)
		return
	}

	// Clear deadline for data transmission
	conn.SetDeadline(time.Time{})

	// 3. Connect to Worker via WebSocket
	log.Printf("[Easy-Net] Connecting to tunnel for target -> %s:%d", host, port)
	wsConn, err := connectToWorker(host, port, config)
	if err != nil {
		log.Printf("[Easy-Net] Tunnel connection failed to %s:%d: %v", host, port, err)
		// Send connection failure response to SOCKS5 client
		conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer wsConn.Close()

	// 4. Send success response to SOCKS5 client
	_, err = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	if err != nil {
		log.Printf("[Easy-Net] Failed to send CONNECT success response: %v", err)
		return
	}

	log.Printf("[Easy-Net] Tunnel established -> %s:%d", host, port)

	// 5. Pipe bi-directionally
	var wg sync.WaitGroup
	wg.Add(2)

	// Local Client -> WebSocket
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
				err = wsConn.WriteMessage(websocket.BinaryMessage, buf[:n])
				if err != nil {
					log.Printf("[Easy-Net] WS write error: %v", err)
					break
				}
			}
		}
	}()

	// WebSocket -> Local Client
	go func() {
		defer wg.Done()
		defer conn.Close()
		for {
			msgType, p, err := wsConn.ReadMessage()
			if err != nil {
				break
			}
			if msgType == websocket.BinaryMessage || msgType == websocket.TextMessage {
				_, err = conn.Write(p)
				if err != nil {
					log.Printf("[Easy-Net] Local write error: %v", err)
					break
				}
			}
		}
	}()

	wg.Wait()
	log.Printf("[Easy-Net] Tunnel closed -> %s:%d", host, port)
}

func socks5Handshake(conn net.Conn) error {
	buf := make([]byte, 257)
	_, err := io.ReadFull(conn, buf[:2])
	if err != nil {
		return err
	}

	version := buf[0]
	nmethods := int(buf[1])

	if version != 5 {
		return fmt.Errorf("invalid SOCKS version: %d", version)
	}

	_, err = io.ReadFull(conn, buf[:nmethods])
	if err != nil {
		return err
	}

	// Respond with version 5, no authentication (0x00)
	_, err = conn.Write([]byte{0x05, 0x00})
	return err
}

func socks5ParseRequest(conn net.Conn) (string, uint16, error) {
	buf := make([]byte, 4)
	_, err := io.ReadFull(conn, buf)
	if err != nil {
		return "", 0, err
	}

	version := buf[0]
	cmd := buf[1]
	atyp := buf[3]

	if version != 5 {
		return "", 0, fmt.Errorf("invalid SOCKS version in request: %d", version)
	}

	if cmd != 1 { // CONNECT
		return "", 0, fmt.Errorf("unsupported command: %d", cmd)
	}

	var host string
	var port uint16

	switch atyp {
	case 0x01: // IPv4
		ipBuf := make([]byte, 4)
		_, err = io.ReadFull(conn, ipBuf)
		if err != nil {
			return "", 0, err
		}
		host = net.IP(ipBuf).String()
	case 0x03: // Domain name
		lenBuf := make([]byte, 1)
		_, err = io.ReadFull(conn, lenBuf)
		if err != nil {
			return "", 0, err
		}
		domainLen := int(lenBuf[0])
		domainBuf := make([]byte, domainLen)
		_, err = io.ReadFull(conn, domainBuf)
		if err != nil {
			return "", 0, err
		}
		host = string(domainBuf)
	case 0x04: // IPv6
		ipBuf := make([]byte, 16)
		_, err = io.ReadFull(conn, ipBuf)
		if err != nil {
			return "", 0, err
		}
		host = net.IP(ipBuf).String()
	default:
		return "", 0, fmt.Errorf("unsupported address type: %d", atyp)
	}

	portBuf := make([]byte, 2)
	_, err = io.ReadFull(conn, portBuf)
	if err != nil {
		return "", 0, err
	}
	port = binary.BigEndian.Uint16(portBuf)

	return host, port, nil
}

func connectToWorker(host string, port uint16, config *Config) (*websocket.Conn, error) {
	resolvedHost := config.WorkerHost

	// 如果配置文件中显式配置了优选 IP，直接使用该 IP 建立连接
	if config.EndpointIP != "" {
		resolvedHost = config.EndpointIP
		log.Printf("[Easy-Net] [优选IP] 正在使用配置的优选物理 IP 连接: %s", resolvedHost)
	} else if !isIP(config.WorkerHost) {
		log.Printf("[Easy-Net] [DNS] 正在通过直连 DNS (223.5.5.5) 解析云端中继域名: %s", config.WorkerHost)
		r := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{
					Timeout: 5 * time.Second,
				}
				return d.DialContext(ctx, "udp", "223.5.5.5:53")
			},
		}
		ips, err := r.LookupHost(context.Background(), config.WorkerHost)
		if err == nil && len(ips) > 0 {
			resolvedHost = ips[0]
			log.Printf("[Easy-Net] [DNS] 解析成功: %s -> %s", config.WorkerHost, resolvedHost)
		} else {
			log.Printf("[Easy-Net] [DNS] 直连解析失败，将使用系统默认解析: %v", err)
		}
	}

	u := url.URL{
		Scheme: "wss",
		Host:   resolvedHost,
		Path:   "/tunnel",
	}
	q := u.Query()
	q.Set("secret", config.Secret)
	q.Set("host", host)
	q.Set("port", strconv.Itoa(int(port)))
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			ServerName: config.WorkerHost, // 关键：确保 TLS 证书校验能匹配正确的域名
		},
	}

	headers := make(http.Header)
	// IMPORTANT: Add User-Agent header to avoid CloudFront 403 blocks!
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
