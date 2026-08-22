package proxy

import (
	"context"
	"net"
	"strings"
	"time"
)

const (
	privateLookupTimeout = 2 * time.Second
	secureLookupTimeout  = 7 * time.Second
)

type hostResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// resolvePrivateTarget reports whether address should use the local network
// stack. Only local-style host names are resolved through Windows DNS. Public
// host names must never be classified with an untrusted local resolver because
// a poisoned answer could otherwise turn a foreign site into a direct route.
func resolvePrivateTarget(ctx context.Context, address string) (string, bool) {
	return resolveBypassTargetWithResolver(ctx, address, true, false, nil)
}

func resolveBypassTarget(ctx context.Context, address string, bypassPrivate, bypassChina bool) (string, bool) {
	return resolveBypassTargetWithResolver(ctx, address, bypassPrivate, bypassChina, nil)
}

// resolveBypassTargetWithResolver applies fail-closed split routing:
//   - literal private/CN IPs can be routed directly without DNS;
//   - local-style names may use the system resolver for private-network access;
//   - public names may use only the resolver reached through the configured
//     proxy transport when deciding whether a CN direct route is safe.
//
// If secureResolver is unavailable or returns a mixed/non-CN result, the
// original hostname is kept and sent through the proxy for remote resolution.
func resolveBypassTargetWithResolver(ctx context.Context, address string, bypassPrivate, bypassChina bool, secureResolver hostResolver) (string, bool) {
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

	if bypassPrivate && isLocalStyleHostname(host) {
		if addresses := lookupHost(ctx, net.DefaultResolver, host, privateLookupTimeout); allAddressesMatch(addresses, true, false) {
			return net.JoinHostPort(addresses[0].String(), port), true
		}
	}

	if bypassChina && secureResolver != nil {
		if addresses := lookupHost(ctx, secureResolver, host, secureLookupTimeout); allAddressesMatch(addresses, false, true) {
			// The secure resolver prefers IPv4. Pinning the chosen answer prevents a
			// second, potentially polluted Windows lookup during the direct dial.
			return net.JoinHostPort(addresses[0].String(), port), true
		}
	}
	return "", false
}

func lookupHost(ctx context.Context, resolver hostResolver, host string, timeout time.Duration) []net.IPAddr {
	lookupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	addresses, err := resolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return nil
	}
	return addresses
}

func allAddressesMatch(addresses []net.IPAddr, bypassPrivate, bypassChina bool) bool {
	if len(addresses) == 0 {
		return false
	}
	for _, resolved := range addresses {
		if !matchesDirectRule(resolved.IP, bypassPrivate, bypassChina) {
			return false
		}
	}
	return true
}

func isLocalStyleHostname(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" || !strings.Contains(host, ".") {
		return host != ""
	}
	for _, suffix := range []string{".localhost", ".local", ".lan", ".internal", ".localdomain", ".home.arpa"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
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
