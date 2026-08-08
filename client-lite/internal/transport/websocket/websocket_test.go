package websocket

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gorillaws "github.com/gorilla/websocket"
)

func TestDialContextUsesEasyNetProtocolAndRelaysBytes(t *testing.T) {
	serverErrors := make(chan error, 1)
	upgrader := gorillaws.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("secret") != "token" || r.URL.Query().Get("host") != "example.com" || r.URL.Query().Get("port") != "443" {
			http.Error(w, "unexpected tunnel query", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "missing authorization header", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer conn.Close()
		kind, payload, err := conn.ReadMessage()
		if err != nil {
			serverErrors <- err
			return
		}
		serverErrors <- conn.WriteMessage(kind, payload)
	}))
	defer server.Close()

	transport, err := New(Config{URL: "ws" + strings.TrimPrefix(server.URL, "http") + "/tunnel", Secret: "token", AllowInsecure: true, LegacyQueryAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := transport.DialContext(ctx, "tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := []byte("through-websocket")
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if string(reply) != string(payload) {
		t.Fatalf("unexpected reply %q", reply)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestNewDefaultsToSecureWebSocket(t *testing.T) {
	transport, err := New(Config{URL: "example.com", Secret: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if transport.url.Scheme != "wss" || transport.url.Path != "/tunnel" {
		t.Fatalf("unexpected normalized URL: %s", transport.url.String())
	}
}

func TestSecureHeaderProtocolDoesNotPutSecretInURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("secret") != "" || strings.Contains(r.URL.RawQuery, "token") {
			http.Error(w, "secret leaked into query", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("X-Target-Host") != "example.com" || r.Header.Get("X-Target-Port") != "443" {
			http.Error(w, "missing secure tunnel headers", http.StatusBadRequest)
			return
		}
		conn, err := (&gorillaws.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()

	transport, err := New(Config{URL: "ws" + strings.TrimPrefix(server.URL, "http") + "/tunnel", Secret: "token", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := transport.DialContext(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

func TestDialContextWaitsForV2TargetReady(t *testing.T) {
	clientReady := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(tunnelProtocolHeader) != tunnelProtocolV2 {
			http.Error(w, "missing protocol header", http.StatusBadRequest)
			return
		}
		conn, err := (&gorillaws.Upgrader{}).Upgrade(w, r, http.Header{
			tunnelProtocolHeader: []string{tunnelProtocolV2},
		})
		if err != nil {
			return
		}
		defer conn.Close()
		select {
		case <-clientReady:
			t.Error("DialContext returned before the target was ready")
		default:
		}
		if err := conn.WriteMessage(gorillaws.TextMessage, []byte(tunnelReadyMessage)); err != nil {
			return
		}
		kind, payload, err := conn.ReadMessage()
		if err == nil {
			_ = conn.WriteMessage(kind, payload)
		}
	}))
	defer server.Close()

	transport, err := New(Config{URL: "ws" + strings.TrimPrefix(server.URL, "http"), Secret: "token", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := transport.DialContext(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	close(clientReady)
	defer conn.Close()
	if _, err := conn.Write([]byte("ready-relay")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len("ready-relay"))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if string(reply) != "ready-relay" {
		t.Fatalf("unexpected reply %q", reply)
	}
}

func TestDialContextReturnsV2TargetError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&gorillaws.Upgrader{}).Upgrade(w, r, http.Header{
			tunnelProtocolHeader: []string{tunnelProtocolV2},
		})
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(gorillaws.TextMessage, []byte(tunnelErrorPrefix+"connection refused"))
	}))
	defer server.Close()

	transport, err := New(Config{URL: "ws" + strings.TrimPrefix(server.URL, "http"), Secret: "token", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.DialContext(context.Background(), "tcp", "example.com:443")
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("unexpected target error: %v", err)
	}
}

func TestNewRejectsInsecureAndCredentialURLs(t *testing.T) {
	if _, err := New(Config{URL: "ws://example.com", Secret: "token"}); err == nil {
		t.Fatal("expected insecure websocket rejection")
	}
	if _, err := New(Config{URL: "wss://user:password@example.com", Secret: "token"}); err == nil {
		t.Fatal("expected URL credentials rejection")
	}
	if _, err := New(Config{URL: "wss://example.com/tunnel?secret=leaked", Secret: "token"}); err == nil {
		t.Fatal("expected query secret rejection")
	}
}

func TestTransportCloseClosesActiveConnections(t *testing.T) {
	serverClosed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&gorillaws.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
		serverClosed <- struct{}{}
	}))
	defer server.Close()
	transport, err := New(Config{URL: "ws" + strings.TrimPrefix(server.URL, "http"), Secret: "token", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := transport.DialContext(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-serverClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("active websocket connection was not closed")
	}
	if _, err := conn.Write([]byte("closed")); err == nil {
		t.Fatal("write unexpectedly succeeded after transport close")
	}
}
