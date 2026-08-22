package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type resolverTestTransport struct{}

func (resolverTestTransport) Start(context.Context) error { return nil }
func (resolverTestTransport) Close() error                { return nil }
func (resolverTestTransport) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func TestSecureHostResolverUsesDoHAndCachesAnswer(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		time.Sleep(30 * time.Millisecond)
		if request.URL.Query().Get("name") != "example.cn" || request.URL.Query().Get("type") != "1" {
			http.Error(response, "unexpected query", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/dns-json")
		_, _ = fmt.Fprint(response, `{"Status":0,"Answer":[{"name":"example.cn.","type":1,"TTL":120,"data":"1.0.1.1"}]}`)
	}))
	defer server.Close()

	originalEndpoints := secureDNSEndpoints
	secureDNSEndpoints = []string{server.URL}
	defer func() { secureDNSEndpoints = originalEndpoints }()

	resolver := newSecureHostResolver(resolverTestTransport{})
	defer resolver.CloseIdleConnections()
	var wait sync.WaitGroup
	errs := make(chan error, 8)
	for attempt := 0; attempt < 8; attempt++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			addresses, err := resolver.LookupIPAddr(context.Background(), "example.cn")
			if err == nil && (len(addresses) != 1 || addresses[0].IP.String() != "1.0.1.1") {
				err = fmt.Errorf("unexpected secure DNS answer: %#v", addresses)
			}
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	// A later lookup must use the positive TTL cache as well.
	for attempt := 0; attempt < 2; attempt++ {
		addresses, err := resolver.LookupIPAddr(context.Background(), "example.cn")
		if err != nil {
			t.Fatal(err)
		}
		if len(addresses) != 1 || addresses[0].IP.String() != "1.0.1.1" {
			t.Fatalf("unexpected secure DNS answer: %#v", addresses)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("secure DNS cache missed: requests=%d", requests.Load())
	}
}

func TestSecureHostResolverRejectsInvalidHostname(t *testing.T) {
	resolver := newSecureHostResolver(resolverTestTransport{})
	defer resolver.CloseIdleConnections()
	if _, err := resolver.LookupIPAddr(context.Background(), "bad host"); err == nil {
		t.Fatal("invalid hostname was accepted")
	}
}
