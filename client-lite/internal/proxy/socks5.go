package proxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"easy-net/client-lite/internal/transport"
)

const (
	maxConcurrentClients      = 256
	maxHTTPConnectHeaderBytes = 32 * 1024
	relayDrainTimeout         = 30 * time.Second
)

type proxyProtocol byte

const (
	protocolSOCKS5 proxyProtocol = iota
	protocolHTTPConnect
)

type DialResultHandler func(target string, err error)

type Server struct {
	address      string
	transport    transport.Transport
	onDialResult DialResultHandler
	listener     net.Listener
	cancel       context.CancelFunc
	running      atomic.Bool
	mu           sync.Mutex
	clients      map[net.Conn]struct{}
	wg           sync.WaitGroup
}

func NewServer(address string, outbound transport.Transport, handlers ...DialResultHandler) *Server {
	server := &Server{address: address, transport: outbound, clients: make(map[net.Conn]struct{})}
	if len(handlers) > 0 {
		server.onDialResult = handlers[0]
	}
	return server
}

func (s *Server) Start(ctx context.Context) error {
	if err := s.transport.Start(ctx); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		_ = s.transport.Close()
		return fmt.Errorf("监听 %s：%w", s.address, err)
	}
	serverCtx, cancel := context.WithCancel(context.Background())
	s.listener = listener
	s.cancel = cancel
	s.running.Store(true)
	s.wg.Add(1)
	go s.acceptLoop(serverCtx)
	return nil
}

func (s *Server) Running() bool { return s.running.Load() }

func (s *Server) Address() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.address
}

func (s *Server) Stop() {
	if !s.running.Swap(false) {
		_ = s.transport.Close()
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.mu.Lock()
	for conn := range s.clients {
		_ = conn.Close()
	}
	s.mu.Unlock()
	_ = s.transport.Close()
	s.wg.Wait()
}

func (s *Server) acceptLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() == nil {
				s.running.Store(false)
				if s.cancel != nil {
					s.cancel()
				}
				_ = s.transport.Close()
			}
			return
		}
		s.mu.Lock()
		if len(s.clients) >= maxConcurrentClients {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.clients[conn] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			defer func() {
				s.mu.Lock()
				delete(s.clients, conn)
				s.mu.Unlock()
			}()
			s.handle(ctx, conn)
		}()
	}
}

func (s *Server) handle(ctx context.Context, local net.Conn) {
	_ = local.SetDeadline(time.Now().Add(15 * time.Second))
	reader := bufio.NewReader(local)
	protocol, target, err := readProxyRequest(local, reader)
	if err != nil {
		return
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	remote, err := s.transport.DialContext(dialCtx, "tcp", target)
	cancel()
	if s.onDialResult != nil {
		s.onDialResult(target, err)
	}
	if err != nil {
		writeConnectFailure(local, protocol)
		return
	}
	defer remote.Close()
	_ = local.SetDeadline(time.Time{})
	writeConnectSuccess(local, protocol)

	done := make(chan struct{}, 2)
	// 使用同一个 bufio.Reader，确保 CONNECT 请求之后被预读的 TLS 数据不会丢失。
	go copyAndCloseWrite(remote, reader, done)
	go copyAndCloseWrite(local, remote, done)
	<-done

	// 一侧读到 EOF 只表示该方向已经发送完毕，不代表反方向也没有数据。
	// 保留连接让剩余响应完成；如果对端一直不结束，再用超时回收连接。
	timer := time.NewTimer(relayDrainTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
	case <-timer.C:
	}
}

func readProxyRequest(conn net.Conn, reader *bufio.Reader) (proxyProtocol, string, error) {
	first, err := reader.Peek(1)
	if err != nil {
		return protocolSOCKS5, "", err
	}
	if first[0] == 0x05 {
		if err := handshake(conn, reader); err != nil {
			return protocolSOCKS5, "", err
		}
		target, err := parseRequest(reader)
		if err != nil {
			writeReply(conn, 0x07)
			return protocolSOCKS5, "", err
		}
		return protocolSOCKS5, target, nil
	}
	target, status, err := parseHTTPConnect(reader)
	if err != nil {
		writeHTTPReply(conn, status)
		return protocolHTTPConnect, "", err
	}
	return protocolHTTPConnect, target, nil
}

func handshake(conn net.Conn, reader io.Reader) error {
	head := make([]byte, 2)
	if _, err := io.ReadFull(reader, head); err != nil {
		return err
	}
	if head[0] != 0x05 || head[1] == 0 {
		return fmt.Errorf("无效的 SOCKS5 握手")
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return err
	}
	for _, method := range methods {
		if method == 0x00 {
			_, err := conn.Write([]byte{0x05, 0x00})
			return err
		}
	}
	_, _ = conn.Write([]byte{0x05, 0xff})
	return fmt.Errorf("客户端未提供免认证方式")
}

func parseRequest(reader io.Reader) (string, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(reader, head); err != nil {
		return "", err
	}
	if head[0] != 0x05 || head[1] != 0x01 {
		return "", fmt.Errorf("仅支持 SOCKS5 CONNECT")
	}
	var host string
	switch head[3] {
	case 0x01:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		host = net.IP(buf).String()
	case 0x03:
		length := []byte{0}
		if _, err := io.ReadFull(reader, length); err != nil {
			return "", err
		}
		if length[0] == 0 {
			return "", fmt.Errorf("域名不能为空")
		}
		buf := make([]byte, int(length[0]))
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		host = string(buf)
	case 0x04:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		host = net.IP(buf).String()
	default:
		return "", fmt.Errorf("不支持的地址类型")
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBytes)
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func parseHTTPConnect(reader *bufio.Reader) (string, int, error) {
	total := 0
	requestLine, err := readHTTPLine(reader, &total)
	if err != nil {
		return "", 400, err
	}
	parts := strings.Fields(requestLine)
	if len(parts) != 3 {
		return "", 400, fmt.Errorf("无效的 HTTP 代理请求行")
	}
	if parts[0] != "CONNECT" {
		return "", 405, fmt.Errorf("HTTP 代理仅支持 CONNECT")
	}
	if parts[2] != "HTTP/1.0" && parts[2] != "HTTP/1.1" {
		return "", 400, fmt.Errorf("不支持的 HTTP 版本")
	}
	target, err := normalizeConnectTarget(parts[1])
	if err != nil {
		return "", 400, err
	}
	for {
		line, err := readHTTPLine(reader, &total)
		if err != nil {
			return "", 400, err
		}
		if line == "" {
			break
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || !strings.Contains(line, ":") {
			return "", 400, fmt.Errorf("无效的 HTTP 代理请求头")
		}
	}
	return target, 0, nil
}

func readHTTPLine(reader *bufio.Reader, total *int) (string, error) {
	var line []byte
	for {
		part, more, err := reader.ReadLine()
		if err != nil {
			return "", err
		}
		*total += len(part) + 2
		if *total > maxHTTPConnectHeaderBytes {
			return "", fmt.Errorf("HTTP CONNECT 请求头过大")
		}
		line = append(line, part...)
		if !more {
			return string(line), nil
		}
	}
}

func normalizeConnectTarget(target string) (string, error) {
	host, portText, err := net.SplitHostPort(target)
	if err != nil || host == "" {
		return "", fmt.Errorf("无效的 CONNECT 目标地址")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("无效的 CONNECT 目标端口")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func writeReply(conn net.Conn, code byte) {
	_, _ = conn.Write([]byte{0x05, code, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}

func writeConnectSuccess(conn net.Conn, protocol proxyProtocol) {
	if protocol == protocolHTTPConnect {
		writeHTTPReply(conn, 200)
		return
	}
	writeReply(conn, 0x00)
}

func writeConnectFailure(conn net.Conn, protocol proxyProtocol) {
	if protocol == protocolHTTPConnect {
		writeHTTPReply(conn, 502)
		return
	}
	writeReply(conn, 0x05)
}

func writeHTTPReply(conn net.Conn, status int) {
	reason := map[int]string{
		200: "Connection Established",
		400: "Bad Request",
		405: "Method Not Allowed",
		502: "Bad Gateway",
	}[status]
	if reason == "" {
		status = 500
		reason = "Internal Server Error"
	}
	if status == 200 {
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\n\r\n", status, reason)
		return
	}
	allow := ""
	if status == 405 {
		allow = "Allow: CONNECT\r\n"
	}
	_, _ = fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\n%sConnection: close\r\nContent-Length: 0\r\n\r\n", status, reason, allow)
}

func copyAndCloseWrite(dst net.Conn, src io.Reader, done chan<- struct{}) {
	_, _ = io.Copy(dst, src)
	if closer, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}
	done <- struct{}{}
}
