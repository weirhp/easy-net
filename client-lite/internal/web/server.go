package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/service"
	"easy-net/client-lite/internal/version"
)

const listenAddress = "127.0.0.1:18081"

//go:embed web/index.html web/app.css web/app.js
var assets embed.FS

type Server struct {
	service  *service.Service
	http     *http.Server
	listener net.Listener
	token    string
	onQuit   func()
}

type stateResponse struct {
	Profiles   []service.ProfileState `json:"profiles"`
	ConfigPath string                 `json:"configPath"`
	Token      string                 `json:"token"`
	Version    string                 `json:"version"`
}

type upsertRequest struct {
	Profile         model.Profile `json:"profile"`
	WebSocketSecret string        `json:"websocketSecret"`
	SSHPassword     string        `json:"sshPassword"`
	SSHPassphrase   string        `json:"sshPassphrase"`
	SSHPrivateKey   string        `json:"sshPrivateKey"`
}

func New(svc *service.Service, onQuit func()) (*Server, error) {
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("生成本地管理令牌：%w", err)
	}
	s := &Server{service: svc, token: hex.EncodeToString(tokenBytes), onQuit: onQuit}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleAsset)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/profiles", s.handleProfiles)
	mux.HandleFunc("/api/profiles/", s.handleProfileAction)
	mux.HandleFunc("/api/start-all", s.handleStartAll)
	mux.HandleFunc("/api/stop-all", s.handleStopAll)
	mux.HandleFunc("/api/app/quit", s.handleQuit)
	s.http = &http.Server{
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s, nil
}

func (s *Server) Start() error {
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("启动本地管理服务 %s：%w", listenAddress, err)
	}
	s.listener = listener
	s.http.Addr = listener.Addr().String()
	go func() {
		if err := s.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[Easy-Net Lite] 管理服务异常退出：%v", err)
			if s.onQuit != nil {
				s.onQuit()
			}
		}
	}()
	return nil
}

func (s *Server) URL() string {
	return "http://" + s.http.Addr
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) Handler() http.Handler { return s.http.Handler }

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := ""
	switch r.URL.Path {
	case "/":
		path = "web/index.html"
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case "/app.css":
		path = "web/app.css"
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case "/app.js":
		path = "web/app.js"
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	default:
		http.NotFound(w, r)
		return
	}
	data, err := assets.ReadFile(path)
	if err != nil {
		http.Error(w, "asset unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, stateResponse{
		Profiles: s.service.States(), ConfigPath: s.service.ConfigPath(), Token: s.token, Version: version.Value,
	})
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusForbidden, "本地管理令牌无效，请刷新页面")
		return
	}
	var request upsertRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	err := s.service.Upsert(request.Profile, service.SecretValues{
		WebSocketSecret: request.WebSocketSecret,
		SSHPassword:     request.SSHPassword,
		SSHPassphrase:   request.SSHPassphrase,
		SSHPrivateKey:   []byte(request.SSHPrivateKey),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleProfileAction(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusForbidden, "本地管理令牌无效，请刷新页面")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/profiles/"), "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if err := s.service.Delete(id); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var err error
	switch parts[1] {
	case "start":
		err = s.service.Start(id)
	case "stop":
		s.service.Stop(id)
	case "trust":
		var body struct {
			Fingerprint string `json:"fingerprint"`
		}
		if decodeErr := decodeJSON(w, r, &body); decodeErr != nil {
			err = decodeErr
		} else if strings.TrimSpace(body.Fingerprint) == "" {
			err = fmt.Errorf("SSH 指纹不能为空")
		} else {
			err = s.service.TrustSSHHost(id, strings.TrimSpace(body.Fingerprint))
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		if unknown, ok := service.AsUnknownHostKey(err); ok {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": err.Error(), "code": "ssh_host_unknown", "address": unknown.Address, "fingerprint": unknown.Fingerprint,
			})
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleStartAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusForbidden, "本地管理令牌无效，请刷新页面")
		return
	}
	go s.service.StartAll()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleStopAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusForbidden, "本地管理令牌无效，请刷新页面")
		return
	}
	s.service.StopAll()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusForbidden, "本地管理令牌无效，请刷新页面")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	if s.onQuit != nil {
		go s.onQuit()
	}
}

func (s *Server) authorized(r *http.Request) bool {
	return r.Header.Get("X-Easy-Net-Token") == s.token
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	body := http.MaxBytesReader(w, r.Body, 256*1024)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("请求内容无效：%w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("请求只能包含一个 JSON 对象")
	}
	return nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
