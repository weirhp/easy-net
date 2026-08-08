package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"easy-net/client-lite/internal/transport"
)

const (
	maxConcurrentClients = 256
	relayDrainTimeout    = 30 * time.Second
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
	if err := handshake(local); err != nil {
		return
	}
	target, err := parseRequest(local)
	if err != nil {
		writeReply(local, 0x07)
		return
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	remote, err := s.transport.DialContext(dialCtx, "tcp", target)
	cancel()
	if s.onDialResult != nil {
		s.onDialResult(target, err)
	}
	if err != nil {
		writeReply(local, 0x05)
		return
	}
	defer remote.Close()
	_ = local.SetDeadline(time.Time{})
	writeReply(local, 0x00)

	done := make(chan struct{}, 2)
	go copyAndCloseWrite(remote, local, done)
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

func handshake(conn net.Conn) error {
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return err
	}
	if head[0] != 0x05 || head[1] == 0 {
		return fmt.Errorf("无效的 SOCKS5 握手")
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
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

func parseRequest(conn net.Conn) (string, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return "", err
	}
	if head[0] != 0x05 || head[1] != 0x01 {
		return "", fmt.Errorf("仅支持 SOCKS5 CONNECT")
	}
	var host string
	switch head[3] {
	case 0x01:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return "", err
		}
		host = net.IP(buf).String()
	case 0x03:
		length := []byte{0}
		if _, err := io.ReadFull(conn, length); err != nil {
			return "", err
		}
		if length[0] == 0 {
			return "", fmt.Errorf("域名不能为空")
		}
		buf := make([]byte, int(length[0]))
		if _, err := io.ReadFull(conn, buf); err != nil {
			return "", err
		}
		host = string(buf)
	case 0x04:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return "", err
		}
		host = net.IP(buf).String()
	default:
		return "", fmt.Errorf("不支持的地址类型")
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBytes)
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func writeReply(conn net.Conn, code byte) {
	_, _ = conn.Write([]byte{0x05, code, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}

func copyAndCloseWrite(dst net.Conn, src net.Conn, done chan<- struct{}) {
	_, _ = io.Copy(dst, src)
	if closer, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}
	done <- struct{}{}
}
