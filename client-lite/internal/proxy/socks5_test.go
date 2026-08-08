package proxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

type echoTransport struct{ targets chan string }

func (e *echoTransport) Start(context.Context) error { return nil }
func (e *echoTransport) Close() error                { return nil }
func (e *echoTransport) DialContext(_ context.Context, _ string, address string) (net.Conn, error) {
	e.targets <- address
	client, server := net.Pipe()
	go func() { defer server.Close(); _, _ = io.Copy(server, server) }()
	return client, nil
}

type failingTransport struct{ err error }

func (f *failingTransport) Start(context.Context) error { return nil }
func (f *failingTransport) Close() error                { return nil }
func (f *failingTransport) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, f.err
}

type directTransport struct{}

func (d *directTransport) Start(context.Context) error { return nil }
func (d *directTransport) Close() error                { return nil }
func (d *directTransport) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func TestSOCKS5ConnectAndRelay(t *testing.T) {
	outbound := &echoTransport{targets: make(chan string, 1)}
	server := NewServer("127.0.0.1:0", outbound)
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	conn, err := net.DialTimeout("tcp", server.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("unexpected method reply: %v", reply)
	}

	request := append([]byte{0x05, 0x01, 0x00, 0x03, 0x0b}, []byte("example.com")...)
	request = append(request, 0x01, 0xbb)
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}
	connectReply := make([]byte, 10)
	if _, err := io.ReadFull(conn, connectReply); err != nil {
		t.Fatal(err)
	}
	if connectReply[1] != 0x00 {
		t.Fatalf("connect failed: %v", connectReply)
	}
	if target := <-outbound.targets; target != "example.com:443" {
		t.Fatalf("unexpected target %q", target)
	}

	payload := []byte("easy-net")
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatal(err)
	}
	if string(echo) != string(payload) {
		t.Fatalf("unexpected echo %q", echo)
	}
}

func TestHTTPConnectAndRelayBufferedPayload(t *testing.T) {
	outbound := &echoTransport{targets: make(chan string, 1)}
	server := NewServer("127.0.0.1:0", outbound)
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	conn, err := net.DialTimeout("tcp", server.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := "early-tls-payload"
	request := "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\nProxy-Connection: keep-alive\r\n\r\n" + payload
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statusLine, " 200 ") {
		t.Fatalf("unexpected CONNECT response %q", statusLine)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	if target := <-outbound.targets; target != "example.com:443" {
		t.Fatalf("unexpected target %q", target)
	}
	echo := make([]byte, len(payload))
	if _, err := io.ReadFull(reader, echo); err != nil {
		t.Fatal(err)
	}
	if string(echo) != payload {
		t.Fatalf("buffered tunnel payload was lost: %q", echo)
	}
}

func TestHTTPConnectRejectsPlainHTTPProxyRequest(t *testing.T) {
	outbound := &echoTransport{targets: make(chan string, 1)}
	server := NewServer("127.0.0.1:0", outbound)
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	conn, err := net.DialTimeout("tcp", server.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	statusLine, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statusLine, " 405 ") {
		t.Fatalf("unexpected HTTP proxy response %q", statusLine)
	}
}

func TestHTTPConnectReportsDialFailure(t *testing.T) {
	wantErr := errors.New("remote authentication failed")
	results := make(chan error, 1)
	server := NewServer("127.0.0.1:0", &failingTransport{err: wantErr}, func(_ string, err error) {
		results <- err
	})
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	conn, err := net.DialTimeout("tcp", server.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	statusLine, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statusLine, " 502 ") {
		t.Fatalf("unexpected CONNECT failure response %q", statusLine)
	}
	if err := <-results; !errors.Is(err, wantErr) {
		t.Fatalf("unexpected dial result: %v", err)
	}
}

func TestSOCKS5DrainsResponseAfterClientHalfClose(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	backendDone := make(chan error, 1)
	go func() {
		conn, acceptErr := backend.Accept()
		if acceptErr != nil {
			backendDone <- acceptErr
			return
		}
		defer conn.Close()
		request, readErr := io.ReadAll(conn)
		if readErr != nil {
			backendDone <- readErr
			return
		}
		if string(request) != "request" {
			backendDone <- errors.New("unexpected backend request")
			return
		}
		// 模拟客户端上传完请求后，服务端仍需处理一段时间再返回响应。
		time.Sleep(100 * time.Millisecond)
		_, writeErr := conn.Write([]byte("delayed-response"))
		backendDone <- writeErr
	}()

	server := NewServer("127.0.0.1:0", &directTransport{})
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	conn, err := net.DialTimeout("tcp", server.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := establishSOCKS5Tunnel(conn, backend.Addr().String()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		t.Fatal("expected TCP connection")
	}
	if err := tcpConn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("delayed-response"))
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatalf("response was truncated after client half-close: %v", err)
	}
	if string(response) != "delayed-response" {
		t.Fatalf("unexpected response %q", response)
	}
	if err := <-backendDone; err != nil {
		t.Fatal(err)
	}
}

func TestSOCKS5RejectsUnsupportedAuthentication(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- handshake(server, server) }()
	_, _ = client.Write([]byte{0x05, 0x01, 0x02})
	reply := make([]byte, 2)
	_, _ = io.ReadFull(client, reply)
	if reply[1] != 0xff {
		t.Fatalf("unexpected reply: %v", reply)
	}
	if err := <-done; err == nil {
		t.Fatal("expected handshake error")
	}
}

func TestSOCKS5ReportsDialFailure(t *testing.T) {
	wantErr := errors.New("remote authentication failed")
	results := make(chan struct {
		target string
		err    error
	}, 1)
	server := NewServer("127.0.0.1:0", &failingTransport{err: wantErr}, func(target string, err error) {
		results <- struct {
			target string
			err    error
		}{target: target, err: err}
	})
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	conn, err := net.DialTimeout("tcp", server.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte{0x05, 0x01, 0x00})
	methodReply := make([]byte, 2)
	if _, err := io.ReadFull(conn, methodReply); err != nil {
		t.Fatal(err)
	}
	request := append([]byte{0x05, 0x01, 0x00, 0x03, 0x0b}, []byte("example.com")...)
	request = append(request, 0x01, 0xbb)
	_, _ = conn.Write(request)
	connectReply := make([]byte, 10)
	if _, err := io.ReadFull(conn, connectReply); err != nil {
		t.Fatal(err)
	}
	if connectReply[1] != 0x05 {
		t.Fatalf("unexpected connect reply: %v", connectReply)
	}
	select {
	case result := <-results:
		if result.target != "example.com:443" || !errors.Is(result.err, wantErr) {
			t.Fatalf("unexpected dial result: %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("dial result was not reported")
	}
}

func establishSOCKS5Tunnel(conn net.Conn, target string) error {
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	methodReply := make([]byte, 2)
	if _, err := io.ReadFull(conn, methodReply); err != nil {
		return err
	}
	if methodReply[0] != 0x05 || methodReply[1] != 0x00 {
		return errors.New("SOCKS5 server rejected authentication method")
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return errors.New("test target must be IPv4")
	}
	request := []byte{0x05, 0x01, 0x00, 0x01, ip[0], ip[1], ip[2], ip[3], byte(port >> 8), byte(port)}
	if _, err := conn.Write(request); err != nil {
		return err
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if reply[1] != 0x00 {
		return errors.New("SOCKS5 CONNECT failed")
	}
	return nil
}
