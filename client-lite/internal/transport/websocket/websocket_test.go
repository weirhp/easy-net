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

	transport, err := New("ws"+strings.TrimPrefix(server.URL, "http")+"/tunnel", "token")
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
	transport, err := New("example.com", "token")
	if err != nil {
		t.Fatal(err)
	}
	if transport.url.Scheme != "wss" || transport.url.Path != "/tunnel" {
		t.Fatalf("unexpected normalized URL: %s", transport.url.String())
	}
}
