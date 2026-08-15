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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"easy-net/client-lite/internal/launch"
	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/service"
	"easy-net/client-lite/internal/sharecode"
	"easy-net/client-lite/internal/version"
)

const listenAddress = "127.0.0.1:18081"

//go:embed web/index.html web/app.css web/app.js
var assets embed.FS

type Options struct {
	ListenAddress string
	StatusFile    string
	Application   string
	DisableAssets bool
	Launches      *launch.Service
}

type Server struct {
	service       *service.Service
	launches      *launch.Service
	http          *http.Server
	listener      net.Listener
	token         string
	onQuit        func()
	listenAddress string
	statusFile    string
	application   string
	serveAssets   bool
}

type stateResponse struct {
	Profiles   []profileView `json:"profiles"`
	Launches   []launch.View `json:"launches,omitempty"`
	Features   featuresView  `json:"features"`
	ConfigPath string        `json:"configPath"`
	Token      string        `json:"token"`
	Version    string        `json:"version"`
	Warnings   []string      `json:"warnings,omitempty"`
}

type featuresView struct {
	AppLaunches bool `json:"appLaunches"`
}

type upsertRequest struct {
	Profile         profileInput `json:"profile"`
	WebSocketSecret string       `json:"websocketSecret"`
	SSHPassword     string       `json:"sshPassword"`
	SSHPassphrase   string       `json:"sshPassphrase"`
	SSHPrivateKey   string       `json:"sshPrivateKey"`
}

type profileInput struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Type          model.ProxyType `json:"type"`
	ListenHost    string          `json:"listenHost"`
	ListenPort    int             `json:"listenPort"`
	AutoStart     bool            `json:"autoStart"`
	BypassPrivate bool            `json:"bypassPrivate"`
	WebSocket     *webSocketInput `json:"websocket"`
	SSH           *sshInput       `json:"ssh"`
}

type webSocketInput struct {
	URL             string `json:"url"`
	AllowInsecure   bool   `json:"allowInsecure"`
	LegacyQueryAuth bool   `json:"legacyQueryAuth"`
}

type sshInput struct {
	Host     string         `json:"host"`
	Port     int            `json:"port"`
	Username string         `json:"username"`
	AuthType model.AuthType `json:"authType"`
}

type profileView struct {
	Profile          publicProfile `json:"profile"`
	Running          bool          `json:"running"`
	Starting         bool          `json:"starting"`
	Error            string        `json:"error,omitempty"`
	ConnectionStatus string        `json:"connectionStatus,omitempty"`
	ConnectionError  string        `json:"connectionError,omitempty"`
	ConnectionAt     string        `json:"connectionAt,omitempty"`
}

type publicProfile struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Type          model.ProxyType  `json:"type"`
	ListenHost    string           `json:"listenHost"`
	ListenPort    int              `json:"listenPort"`
	AutoStart     bool             `json:"autoStart"`
	BypassPrivate bool             `json:"bypassPrivate"`
	WebSocket     *publicWebSocket `json:"websocket,omitempty"`
	SSH           *publicSSH       `json:"ssh,omitempty"`
}

type publicWebSocket struct {
	URL             string `json:"url"`
	HasSecret       bool   `json:"hasSecret"`
	AllowInsecure   bool   `json:"allowInsecure"`
	LegacyQueryAuth bool   `json:"legacyQueryAuth"`
}

type publicSSH struct {
	Host          string         `json:"host"`
	Port          int            `json:"port"`
	Username      string         `json:"username"`
	AuthType      model.AuthType `json:"authType"`
	HasPassword   bool           `json:"hasPassword"`
	HasPrivateKey bool           `json:"hasPrivateKey"`
	HasPassphrase bool           `json:"hasPassphrase"`
}

type AlreadyRunningError struct{ URL string }

func (e *AlreadyRunningError) Error() string { return "Easy-Net Lite 已经在运行" }

func New(svc *service.Service, onQuit func()) (*Server, error) {
	return NewWithOptions(svc, onQuit, Options{})
}

func NewWithOptions(svc *service.Service, onQuit func(), options Options) (*Server, error) {
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("生成本地管理令牌：%w", err)
	}
	application := strings.TrimSpace(options.Application)
	if application == "" {
		application = "easy-net-lite"
	}
	listen := strings.TrimSpace(options.ListenAddress)
	if listen == "" {
		listen = listenAddress
	}
	s := &Server{
		service: svc, launches: options.Launches, token: hex.EncodeToString(tokenBytes), onQuit: onQuit,
		listenAddress: listen, statusFile: strings.TrimSpace(options.StatusFile),
		application: application, serveAssets: !options.DisableAssets,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleAsset)
	mux.HandleFunc("/api/ping", s.handlePing)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/proxy", s.handleProxy)
	mux.HandleFunc("/api/profiles", s.handleProfiles)
	mux.HandleFunc("/api/profiles/", s.handleProfileAction)
	mux.HandleFunc("/api/launches", s.handleLaunches)
	mux.HandleFunc("/api/launches/", s.handleLaunchAction)
	mux.HandleFunc("/api/export", s.handleExport)
	mux.HandleFunc("/api/import", s.handleImport)
	mux.HandleFunc("/api/start-all", s.handleStartAll)
	mux.HandleFunc("/api/stop-all", s.handleStopAll)
	mux.HandleFunc("/api/app/quit", s.handleQuit)
	s.http = &http.Server{
		Handler:           securityHeaders(localRequestOnly(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	return s, nil
}

func (s *Server) Start() error {
	if existingURL, ok := probeApplicationStatusFile(s.statusFile, s.application); ok {
		return &AlreadyRunningError{URL: existingURL}
	}
	listener, err := net.Listen("tcp", s.listenAddress)
	if err != nil {
		if existingURL, ok := probeApplicationServerAt(
			"http://"+s.listenAddress, s.application, s.application == "easy-net-lite",
		); ok {
			return &AlreadyRunningError{URL: existingURL}
		}
		log.Printf("[Easy-Net Lite] 管理端口 %s 被占用，将使用随机本地端口：%v", s.listenAddress, err)
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("启动本地管理服务：%w", err)
		}
	}
	s.listener = listener
	s.http.Addr = listener.Addr().String()
	if err := s.writeStatusFile(); err != nil {
		_ = listener.Close()
		return err
	}
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

func probeApplicationStatusFile(path, expectedApplication string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	var status struct {
		Application string `json:"application"`
		Control     string `json:"control"`
	}
	if err := json.NewDecoder(io.LimitReader(file, 64*1024)).Decode(&status); err != nil || status.Application != expectedApplication {
		return "", false
	}
	parsed, err := url.Parse(status.Control)
	if err != nil || parsed.Scheme != "http" || !isLocalHostname(parsed.Hostname()) || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	baseURL := strings.TrimRight(status.Control, "/")
	return probeApplicationServerAt(baseURL, expectedApplication, expectedApplication == "easy-net-lite")
}

func (s *Server) URL() string {
	return "http://" + s.http.Addr
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) Handler() http.Handler { return s.http.Handler }

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	if !s.serveAssets {
		http.NotFound(w, r)
		return
	}
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
	states := s.service.States()
	profiles := make([]profileView, 0, len(states))
	for _, state := range states {
		profiles = append(profiles, toProfileView(state))
	}
	response := stateResponse{
		Profiles: profiles, Features: s.features(), ConfigPath: s.service.ConfigPath(),
		Token: s.token, Version: version.Value, Warnings: s.service.ConfigWarnings(),
	}
	if s.launches != nil {
		response.Launches = s.launches.Views()
		if response.Launches == nil {
			response.Launches = []launch.View{}
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) features() featuresView {
	return featuresView{AppLaunches: s.launches != nil && launch.Supported()}
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"application": s.application, "version": version.Value})
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	summary, ok := s.profileSummary(id)
	if !ok {
		writeError(w, http.StatusNotFound, "代理配置不存在")
		return
	}
	writeJSON(w, http.StatusOK, summary)
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
	err := s.service.Upsert(request.Profile.modelProfile(), service.SecretValues{
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
	result := any(map[string]bool{"ok": true})
	switch parts[1] {
	case "start":
		err = s.service.Start(id)
		if err == nil {
			if summary, ok := s.profileSummary(id); ok {
				summary["ok"] = true
				result = summary
			}
		}
	case "stop":
		s.service.Stop(id)
	case "test":
		err = s.service.TestConnection(id)
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
	case "share":
		var payload sharecode.Payload
		payload, err = s.service.ExportShare(id)
		if err == nil {
			var code string
			code, err = sharecode.Encode(payload)
			if err == nil {
				result = map[string]string{"shareCode": code}
			}
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
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleLaunches(w http.ResponseWriter, r *http.Request) {
	if s.launches == nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"launches": s.launches.Views()})
	case http.MethodPost:
		if !s.authorized(r) {
			writeError(w, http.StatusForbidden, "本地管理令牌无效，请刷新页面")
			return
		}
		var entry model.LaunchEntry
		if err := decodeJSON(w, r, &entry); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		saved, err := s.launches.Upsert(entry)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entry": saved})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleLaunchAction(w http.ResponseWriter, r *http.Request) {
	if s.launches == nil {
		http.NotFound(w, r)
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusForbidden, "本地管理令牌无效，请刷新页面")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/launches/"), "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if err := s.launches.Delete(id); err != nil {
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
	switch parts[1] {
	case "start":
		view, err := s.launches.Start(id)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, launch.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entry": view, "listenAddress": view.ListenAddress})
	case "shortcut":
		path, err := s.launches.CreateShortcut(id)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusForbidden, "本地管理令牌无效，请刷新页面")
		return
	}
	var body struct {
		ShareCode string `json:"shareCode"`
		AutoStart bool   `json:"autoStart"`
		Reuse     bool   `json:"reuse"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	codes := uniqueShareCodes(body.ShareCode)
	if len(codes) == 0 {
		writeError(w, http.StatusBadRequest, "请至少提供一个 Easy-Net Lite 分享码")
		return
	}
	if len(codes) > 50 {
		writeError(w, http.StatusBadRequest, "一次最多导入 50 个分享码")
		return
	}
	payloads := make([]sharecode.Payload, 0, len(codes))
	for index, code := range codes {
		payload, err := sharecode.Decode(code)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 个分享码无效：%v", index+1, err))
			return
		}
		payloads = append(payloads, payload)
	}

	ids := make([]string, 0, len(payloads))
	for index, payload := range payloads {
		var id string
		var err error
		if body.Reuse {
			id, err = s.service.ImportOrReuseShare(payload)
		} else {
			id, err = s.service.ImportShare(payload)
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 个配置导入失败：%v", index+1, err))
			return
		}
		ids = append(ids, id)
		if body.AutoStart {
			if startErr := s.service.Start(id); startErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"ok": false, "id": id, "ids": ids, "imported": len(ids),
					"error": fmt.Sprintf("第 %d 个配置已导入，但启动失败：%v", index+1, startErr),
				})
				return
			}
		}
	}
	if len(ids) > 1 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ids": ids, "imported": len(ids)})
		return
	}
	summary, ok := s.profileSummary(ids[0])
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": ids[0], "ids": ids, "imported": 1})
		return
	}
	summary["ok"] = true
	summary["ids"] = ids
	summary["imported"] = 1
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusForbidden, "本地管理令牌无效，请刷新页面")
		return
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ids := uniqueStrings(body.IDs)
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "请至少选择一个代理配置")
		return
	}
	if len(ids) > 50 {
		writeError(w, http.StatusBadRequest, "一次最多导出 50 个代理配置")
		return
	}
	codes := make([]string, 0, len(ids))
	for _, id := range ids {
		payload, err := s.service.ExportShare(id)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		code, err := sharecode.Encode(payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		codes = append(codes, code)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "shareCodes": codes, "shareCode": strings.Join(codes, "\n"), "exported": len(codes),
	})
}

func uniqueShareCodes(value string) []string {
	return uniqueStrings(strings.FieldsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ';' || r == '；'
	}))
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
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
		w.Header().Set("X-Easy-Net-Lite", "1")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func localRequestOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalHostname(requestHostname(r.Host)) {
			writeError(w, http.StatusMisdirectedRequest, "管理界面仅接受本机地址访问")
			return
		}
		if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
			writeError(w, http.StatusForbidden, "拒绝跨站请求")
			return
		}
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !sameOrigin(origin, r.Host) {
			writeError(w, http.StatusForbidden, "拒绝非同源请求")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestHostname(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(hostport, "[]")
}

func isLocalHostname(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sameOrigin(rawOrigin, requestHost string) bool {
	origin, err := url.Parse(rawOrigin)
	if err != nil || origin.Scheme != "http" || !isLocalHostname(origin.Hostname()) {
		return false
	}
	requestURL, err := url.Parse("http://" + requestHost)
	if err != nil {
		return false
	}
	if !strings.EqualFold(origin.Hostname(), requestURL.Hostname()) {
		originIP, requestIP := net.ParseIP(origin.Hostname()), net.ParseIP(requestURL.Hostname())
		if originIP == nil || requestIP == nil || !originIP.Equal(requestIP) {
			return false
		}
	}
	originPort, requestPort := origin.Port(), requestURL.Port()
	if originPort == "" {
		originPort = "80"
	}
	if requestPort == "" {
		requestPort = "80"
	}
	return originPort == requestPort
}

func (s *Server) writeStatusFile() error {
	if s.statusFile == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.statusFile), 0700); err != nil {
		return fmt.Errorf("创建引擎状态目录：%w", err)
	}
	data, err := json.MarshalIndent(map[string]any{
		"application": s.application,
		"pid":         os.Getpid(),
		"control":     s.URL(),
		"version":     version.Value,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化引擎状态：%w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(s.statusFile, data, 0600); err != nil {
		return fmt.Errorf("写入引擎状态：%w", err)
	}
	return nil
}

func (s *Server) profileSummary(id string) (map[string]any, bool) {
	if id == "" {
		return nil, false
	}
	for _, state := range s.service.States() {
		if state.Profile.ID != id {
			continue
		}
		return map[string]any{
			"id":            state.Profile.ID,
			"name":          state.Profile.Name,
			"listenHost":    state.Profile.ListenHost,
			"listenPort":    state.Profile.ListenPort,
			"listenAddress": state.Profile.ListenAddress(),
			"running":       state.Running,
			"starting":      state.Starting,
			"error":         state.Error,
		}, true
	}
	return nil, false
}

func probeServerAt(baseURL string) (string, bool) {
	return probeApplicationServerAt(baseURL, "easy-net-lite", true)
}

func probeApplicationServerAt(baseURL, expectedApplication string, allowLegacy bool) (string, bool) {
	client := &http.Client{
		Timeout:   750 * time.Millisecond,
		Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext},
	}
	response, err := client.Get(baseURL + "/api/ping")
	if err == nil {
		isCurrent := response.StatusCode == http.StatusOK && response.Header.Get("X-Easy-Net-Lite") == "1"
		var ping struct {
			Application string `json:"application"`
		}
		if isCurrent {
			_ = json.NewDecoder(io.LimitReader(response.Body, 16*1024)).Decode(&ping)
		}
		_ = response.Body.Close()
		if isCurrent && ping.Application == expectedApplication {
			return baseURL, true
		}
		if isCurrent && ping.Application != "" {
			return "", false
		}
	}
	if !allowLegacy {
		return "", false
	}

	// 0.1.1 之前没有 /api/ping 标记；使用旧管理接口的稳定特征识别，避免升级时启动两个实例。
	response, err = client.Get(baseURL + "/api/state")
	if err != nil {
		return "", false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		return "", false
	}
	var legacy struct {
		ConfigPath string `json:"configPath"`
		Token      string `json:"token"`
		Version    string `json:"version"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64*1024))
	if err := decoder.Decode(&legacy); err != nil {
		return "", false
	}
	if legacy.ConfigPath == "" || len(legacy.Token) != 48 || legacy.Version == "" {
		return "", false
	}
	return baseURL, true
}

func (p profileInput) modelProfile() model.Profile {
	profile := model.Profile{ID: p.ID, Name: p.Name, Type: p.Type, ListenHost: p.ListenHost, ListenPort: p.ListenPort, AutoStart: p.AutoStart, BypassPrivate: p.BypassPrivate}
	if p.WebSocket != nil {
		profile.WebSocket = &model.WebSocketConfig{URL: p.WebSocket.URL, AllowInsecure: p.WebSocket.AllowInsecure, LegacyQueryAuth: p.WebSocket.LegacyQueryAuth}
	}
	if p.SSH != nil {
		profile.SSH = &model.SSHConfig{Host: p.SSH.Host, Port: p.SSH.Port, Username: p.SSH.Username, AuthType: p.SSH.AuthType}
	}
	return profile
}

func toProfileView(state service.ProfileState) profileView {
	profile := state.Profile
	view := publicProfile{ID: profile.ID, Name: profile.Name, Type: profile.Type, ListenHost: profile.ListenHost, ListenPort: profile.ListenPort, AutoStart: profile.AutoStart, BypassPrivate: profile.BypassPrivate}
	if profile.WebSocket != nil {
		view.WebSocket = &publicWebSocket{URL: profile.WebSocket.URL, HasSecret: profile.WebSocket.SecretRef != "", AllowInsecure: profile.WebSocket.AllowInsecure, LegacyQueryAuth: profile.WebSocket.LegacyQueryAuth}
	}
	if profile.SSH != nil {
		view.SSH = &publicSSH{Host: profile.SSH.Host, Port: profile.SSH.Port, Username: profile.SSH.Username, AuthType: profile.SSH.AuthType, HasPassword: profile.SSH.AuthType == model.AuthTypePassword && profile.SSH.PasswordRef != "", HasPrivateKey: profile.SSH.PrivateKeyPath != "", HasPassphrase: profile.SSH.AuthType == model.AuthTypePrivateKey && profile.SSH.PassphraseRef != ""}
	}
	connectionAt := ""
	if !state.ConnectionAt.IsZero() {
		connectionAt = state.ConnectionAt.Format(time.RFC3339)
	}
	return profileView{
		Profile: view, Running: state.Running, Starting: state.Starting, Error: state.Error,
		ConnectionStatus: state.ConnectionStatus, ConnectionError: state.ConnectionError, ConnectionAt: connectionAt,
	}
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
