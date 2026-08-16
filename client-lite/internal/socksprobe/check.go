package socksprobe

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

const probeHost = "www.microsoft.com"

// Check verifies a no-auth SOCKS5 endpoint and asks it to connect to a stable
// HTTPS target. A TCP-only port or an HTTP-only proxy is rejected explicitly.
func Check(address string) error {
	connection, err := net.DialTimeout("tcp", address, 2500*time.Millisecond)
	if err != nil {
		return fmt.Errorf("无法连接 SOCKS5 %s：%w", address, err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := connection.Write([]byte{5, 1, 0}); err != nil {
		return fmt.Errorf("SOCKS5 握手发送失败：%w", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(connection, reply); err != nil || reply[0] != 5 || reply[1] != 0 {
		if err != nil {
			return fmt.Errorf("SOCKS5 握手失败：%w", err)
		}
		return fmt.Errorf("SOCKS5 拒绝免认证连接（响应 %d）", reply[1])
	}
	host := []byte(probeHost)
	request := []byte{5, 1, 0, 3, byte(len(host))}
	request = append(request, host...)
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, 443)
	request = append(request, port...)
	if _, err := connection.Write(request); err != nil {
		return fmt.Errorf("SOCKS5 测试请求发送失败：%w", err)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(connection, head); err != nil {
		return fmt.Errorf("SOCKS5 测试响应失败：%w", err)
	}
	if head[0] != 5 || head[1] != 0 {
		return fmt.Errorf("SOCKS5 无法连接测试目标（响应 %d）", head[1])
	}
	var addressBytes int
	switch head[3] {
	case 1:
		addressBytes = 4
	case 4:
		addressBytes = 16
	case 3:
		length := []byte{0}
		if _, err := io.ReadFull(connection, length); err != nil {
			return fmt.Errorf("SOCKS5 测试响应不完整：%w", err)
		}
		addressBytes = int(length[0])
	default:
		return fmt.Errorf("SOCKS5 返回了未知地址类型 %d", head[3])
	}
	if _, err := io.CopyN(io.Discard, connection, int64(addressBytes+2)); err != nil {
		return fmt.Errorf("SOCKS5 测试响应不完整：%w", err)
	}
	return nil
}
