package proxy

import (
	"context"
	"net"
	"strings"
	"time"
)

const privateLookupTimeout = 2 * time.Second

// resolvePrivateTarget reports whether address should use the local network
// stack. Host names are only bypassed when every locally resolved address is
// private, so split-horizon names work without accidentally bypassing a public
// endpoint returned by the same lookup.
func resolvePrivateTarget(ctx context.Context, address string) (string, bool) {
	return resolveBypassTarget(ctx, address, true, false)
}

func resolveBypassTarget(ctx context.Context, address string, bypassPrivate, bypassChina bool) (string, bool) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return "", false
	}

	bareHost, zone := splitZone(host)
	if ip := net.ParseIP(bareHost); ip != nil {
		if !matchesDirectRule(ip, bypassPrivate, bypassChina) {
			return "", false
		}
		if zone != "" {
			bareHost += "%" + zone
		}
		return net.JoinHostPort(bareHost, port), true
	}

	lookupCtx, cancel := context.WithTimeout(ctx, privateLookupTimeout)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil || len(addresses) == 0 {
		return "", false
	}
	for _, resolved := range addresses {
		if !matchesDirectRule(resolved.IP, bypassPrivate, bypassChina) {
			return "", false
		}
	}
	return net.JoinHostPort(addresses[0].String(), port), true
}

func matchesDirectRule(ip net.IP, bypassPrivate, bypassChina bool) bool {
	return bypassPrivate && isPrivateDestination(ip) || bypassChina && isChinaDestination(ip)
}

func splitZone(host string) (string, string) {
	if index := strings.LastIndexByte(host, '%'); index >= 0 {
		return host[:index], host[index+1:]
	}
	return host, ""
}

func isPrivateDestination(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return true
	}
	// RFC 6598 shared address space is commonly used as an internal network by
	// VPN and carrier-grade NAT deployments, but net.IP.IsPrivate excludes it.
	ipv4 := ip.To4()
	return ipv4 != nil && ipv4[0] == 100 && ipv4[1]&0xc0 == 0x40
}
