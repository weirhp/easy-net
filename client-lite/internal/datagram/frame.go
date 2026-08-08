package datagram

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
)

const MaxPayloadSize = 65507

var (
	ErrFragmented = errors.New("不支持分片的 SOCKS5 UDP 数据报")
	ErrMalformed  = errors.New("无效的 UDP 数据报帧")
)

// Encode creates the Easy-Net UDP frame used in one WebSocket binary message:
// ATYP | DST.ADDR | DST.PORT | PAYLOAD. It intentionally matches the address
// portion of an RFC 1928 SOCKS5 UDP request so no session IDs or stream framing
// are needed.
func Encode(address string, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayloadSize {
		return nil, fmt.Errorf("UDP 负载超过 %d 字节", MaxPayloadSize)
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return nil, fmt.Errorf("无效的 UDP 目标地址 %q", address)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("无效的 UDP 目标端口")
	}

	frame := make([]byte, 0, 1+16+2+len(payload))
	if ip := net.ParseIP(host); ip != nil {
		if ipv4 := ip.To4(); ipv4 != nil {
			frame = append(frame, 0x01)
			frame = append(frame, ipv4...)
		} else {
			frame = append(frame, 0x04)
			frame = append(frame, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, fmt.Errorf("UDP 目标域名过长")
		}
		frame = append(frame, 0x03, byte(len(host)))
		frame = append(frame, host...)
	}
	frame = binary.BigEndian.AppendUint16(frame, uint16(port))
	frame = append(frame, payload...)
	return frame, nil
}

func Decode(frame []byte) (address string, payload []byte, err error) {
	if len(frame) < 1 {
		return "", nil, ErrMalformed
	}
	offset := 1
	var host string
	switch frame[0] {
	case 0x01:
		if len(frame) < offset+4+2 {
			return "", nil, ErrMalformed
		}
		host = net.IP(frame[offset : offset+4]).String()
		offset += 4
	case 0x03:
		if len(frame) < offset+1 {
			return "", nil, ErrMalformed
		}
		length := int(frame[offset])
		offset++
		if length == 0 || len(frame) < offset+length+2 {
			return "", nil, ErrMalformed
		}
		host = string(frame[offset : offset+length])
		offset += length
	case 0x04:
		if len(frame) < offset+16+2 {
			return "", nil, ErrMalformed
		}
		host = net.IP(frame[offset : offset+16]).String()
		offset += 16
	default:
		return "", nil, ErrMalformed
	}
	port := binary.BigEndian.Uint16(frame[offset : offset+2])
	if port == 0 {
		return "", nil, ErrMalformed
	}
	offset += 2
	if len(frame)-offset > MaxPayloadSize {
		return "", nil, ErrMalformed
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), frame[offset:], nil
}

func EncodeSOCKS5(address string, payload []byte) ([]byte, error) {
	frame, err := Encode(address, payload)
	if err != nil {
		return nil, err
	}
	packet := make([]byte, 3, 3+len(frame))
	return append(packet, frame...), nil
}

func DecodeSOCKS5(packet []byte) (address string, payload []byte, err error) {
	if len(packet) < 4 || packet[0] != 0 || packet[1] != 0 {
		return "", nil, ErrMalformed
	}
	if packet[2] != 0 {
		return "", nil, ErrFragmented
	}
	return Decode(packet[3:])
}
