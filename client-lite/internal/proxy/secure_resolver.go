package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"easy-net/client-lite/internal/transport"
)

const (
	secureDNSRequestTimeout = 6 * time.Second
	secureDNSMinTTL         = 30 * time.Second
	secureDNSMaxTTL         = 30 * time.Minute
)

var secureDNSEndpoints = []string{
	"https://cloudflare-dns.com/dns-query",
	"https://dns.google/resolve",
}

type cachedDNSAnswer struct {
	addresses []net.IPAddr
	expiresAt time.Time
}

type pendingDNSLookup struct {
	done      chan struct{}
	addresses []net.IPAddr
	err       error
}

// secureHostResolver resolves public names over the configured proxy
// transport. DNS therefore follows the same encrypted WebSocket/SSH path as
// the application instead of trusting the Windows or router DNS response.
type secureHostResolver struct {
	client    *http.Client
	transport *http.Transport
	mu        sync.Mutex
	cache     map[string]cachedDNSAnswer
	pending   map[string]*pendingDNSLookup
}

func newSecureHostResolver(outbound transport.Transport) *secureHostResolver {
	httpTransport := &http.Transport{
		Proxy:                 nil,
		DialContext:           outbound.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
	}
	return &secureHostResolver{
		client:    &http.Client{Transport: httpTransport, Timeout: secureDNSRequestTimeout},
		transport: httpTransport,
		cache:     make(map[string]cachedDNSAnswer),
		pending:   make(map[string]*pendingDNSLookup),
	}
}

func (r *secureHostResolver) CloseIdleConnections() {
	if r != nil && r.transport != nil {
		r.transport.CloseIdleConnections()
	}
}

func (r *secureHostResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if !validDNSHostname(host) {
		return nil, fmt.Errorf("DNS 域名无效")
	}
	now := time.Now()
	r.mu.Lock()
	if cached, ok := r.cache[host]; ok {
		if now.Before(cached.expiresAt) {
			addresses := cloneIPAddresses(cached.addresses)
			r.mu.Unlock()
			return addresses, nil
		}
		delete(r.cache, host)
	}
	if pending := r.pending[host]; pending != nil {
		r.mu.Unlock()
		select {
		case <-pending.done:
			return cloneIPAddresses(pending.addresses), pending.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// Chromium commonly opens several connections to a new host at once. Keep
	// one resolver request per host so startup does not create a burst of DoH
	// tunnels before the first answer enters the cache.
	pending := &pendingDNSLookup{done: make(chan struct{})}
	r.pending[host] = pending
	r.mu.Unlock()

	addresses, ttl, err := r.lookupType(ctx, host, 1)
	if err == nil && len(addresses) == 0 {
		addresses, ttl, err = r.lookupType(ctx, host, 28)
	}
	if err != nil || len(addresses) == 0 {
		if err == nil {
			err = fmt.Errorf("加密 DNS 未返回地址")
		}
		r.finishLookup(host, pending, nil, 0, err, now)
		return nil, err
	}
	if ttl < secureDNSMinTTL {
		ttl = secureDNSMinTTL
	}
	if ttl > secureDNSMaxTTL {
		ttl = secureDNSMaxTTL
	}
	r.finishLookup(host, pending, addresses, ttl, nil, now)
	return addresses, nil
}

func (r *secureHostResolver) finishLookup(host string, pending *pendingDNSLookup, addresses []net.IPAddr, ttl time.Duration, err error, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending.addresses = cloneIPAddresses(addresses)
	pending.err = err
	if err == nil && len(addresses) > 0 {
		r.cache[host] = cachedDNSAnswer{addresses: cloneIPAddresses(addresses), expiresAt: now.Add(ttl)}
	}
	delete(r.pending, host)
	close(pending.done)
}

type jsonDNSResponse struct {
	Status int `json:"Status"`
	Answer []struct {
		Type uint16 `json:"type"`
		TTL  uint32 `json:"TTL"`
		Data string `json:"data"`
	} `json:"Answer"`
}

func (r *secureHostResolver) lookupType(ctx context.Context, host string, queryType uint16) ([]net.IPAddr, time.Duration, error) {
	var lastErr error
	for _, endpoint := range secureDNSEndpoints {
		query, err := url.Parse(endpoint)
		if err != nil {
			continue
		}
		values := query.Query()
		values.Set("name", host)
		values.Set("type", strconv.Itoa(int(queryType)))
		query.RawQuery = values.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, query.String(), nil)
		if err != nil {
			lastErr = err
			continue
		}
		request.Header.Set("Accept", "application/dns-json")
		response, err := r.client.Do(request)
		if err != nil {
			lastErr = err
			continue
		}
		var result jsonDNSResponse
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("DoH HTTP %d", response.StatusCode)
			continue
		}
		if decodeErr != nil {
			lastErr = decodeErr
			continue
		}
		if result.Status != 0 {
			lastErr = fmt.Errorf("DoH 状态 %d", result.Status)
			continue
		}
		addresses := make([]net.IPAddr, 0, len(result.Answer))
		minimumTTL := secureDNSMaxTTL
		for _, answer := range result.Answer {
			if answer.Type != queryType {
				continue
			}
			ip := net.ParseIP(strings.TrimSpace(answer.Data))
			if ip == nil || queryType == 1 && ip.To4() == nil || queryType == 28 && ip.To4() != nil {
				continue
			}
			addresses = append(addresses, net.IPAddr{IP: append(net.IP(nil), ip...)})
			ttl := time.Duration(answer.TTL) * time.Second
			if ttl < minimumTTL {
				minimumTTL = ttl
			}
		}
		return addresses, minimumTTL, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的加密 DNS")
	}
	return nil, 0, lastErr
}

func validDNSHostname(host string) bool {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " /\\:@[]") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character > 127 || character != '-' && (character < '0' || character > '9') && (character < 'a' || character > 'z') {
				return false
			}
		}
	}
	return true
}

func cloneIPAddresses(input []net.IPAddr) []net.IPAddr {
	output := make([]net.IPAddr, len(input))
	for index, address := range input {
		output[index] = net.IPAddr{IP: append(net.IP(nil), address.IP...), Zone: address.Zone}
	}
	return output
}
