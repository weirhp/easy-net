package clashsub

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

const probeHost = "www.gstatic.com"

func probeNodeThroughSOCKS(address string) error {
	connection, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		return fmt.Errorf("连接本地 SOCKS5：%w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := connection.Write([]byte{5, 1, 0}); err != nil {
		return fmt.Errorf("SOCKS5 握手：%w", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(connection, reply); err != nil || reply[0] != 5 || reply[1] != 0 {
		if err != nil {
			return fmt.Errorf("SOCKS5 握手：%w", err)
		}
		return fmt.Errorf("SOCKS5 拒绝免认证连接")
	}
	host := []byte(probeHost)
	request := []byte{5, 1, 0, 3, byte(len(host))}
	request = append(request, host...)
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, 443)
	request = append(request, port...)
	if _, err := connection.Write(request); err != nil {
		return fmt.Errorf("SOCKS5 请求：%w", err)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(connection, head); err != nil {
		return fmt.Errorf("SOCKS5 响应：%w", err)
	}
	if head[0] != 5 || head[1] != 0 {
		return fmt.Errorf("节点未能连通测试目标")
	}
	if err := discardSOCKSAddress(connection, head[3]); err != nil {
		return err
	}
	tlsConn := tls.Client(connection, &tls.Config{ServerName: probeHost, MinVersion: tls.VersionTLS12})
	if err := tlsConn.Handshake(); err != nil {
		return fmt.Errorf("节点 TLS 握手失败：%w", err)
	}
	_ = tlsConn.Close()
	return nil
}

func discardSOCKSAddress(connection net.Conn, atyp byte) error {
	var addressBytes int
	switch atyp {
	case 1:
		addressBytes = 4
	case 4:
		addressBytes = 16
	case 3:
		length := []byte{0}
		if _, err := io.ReadFull(connection, length); err != nil {
			return fmt.Errorf("SOCKS5 响应不完整：%w", err)
		}
		addressBytes = int(length[0])
	default:
		return fmt.Errorf("SOCKS5 返回了未知地址类型 %d", atyp)
	}
	if _, err := io.CopyN(io.Discard, connection, int64(addressBytes+2)); err != nil {
		return fmt.Errorf("SOCKS5 响应不完整：%w", err)
	}
	return nil
}

func lastMihomoConnectError(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		idx := strings.LastIndex(line, "connect error:")
		if idx < 0 {
			continue
		}
		detail := strings.TrimSpace(line[idx+len("connect error:"):])
		detail = strings.Trim(detail, `"`)
		switch {
		case strings.Contains(detail, "dns resolve failed"):
			return "DNS 解析失败"
		case strings.Contains(detail, "EOF"):
			return "入口连接被断开"
		case strings.Contains(detail, "i/o timeout"), strings.Contains(detail, "context deadline exceeded"):
			return "入口连接超时"
		case detail == "":
			continue
		default:
			if len(detail) > 120 {
				detail = detail[:120]
			}
			return detail
		}
	}
	return ""
}
