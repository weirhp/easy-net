package main

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) RegisterRoutes(mux *http.ServeMux, app *AppRuntime) {
	mux.HandleFunc("/", m.handleIndex)
	mux.HandleFunc("/api/state", m.handleState)
	mux.HandleFunc("/api/config", m.handleConfig)
	mux.HandleFunc("/api/easy-net/", m.handleEasyNetAction)
	mux.HandleFunc("/api/mihomo/", m.handleMihomoAction)
	mux.HandleFunc("/api/app/quit", handleAppQuit(app))
	m.RegisterSubscriptionRoutes(mux)
}

func (m *Manager) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := []byte(embeddedIndexHTML)
	for _, name := range []string{"ui.html", filepath.Join("src", "ui.html")} {
		if diskHTML, err := os.ReadFile(filepath.Join(m.workDir, name)); err == nil {
			html = diskHTML
			break
		}
	}
	_, _ = w.Write(html)
}

func (m *Manager) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, m.State())
}

func (m *Manager) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var cfg AppConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := m.SaveAndApply(&cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (m *Manager) handleEasyNetAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/easy-net/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	id, action := parts[0], parts[1]
	var err error
	switch action {
	case "start":
		err = m.StartEasyNet(id)
	case "stop":
		err = m.StopEasyNet(id)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (m *Manager) handleMihomoAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/mihomo/")
	var err error
	var path string
	switch action {
	case "start":
		err = m.StartMihomo()
	case "stop":
		err = m.StopMihomo()
	case "restart":
		err = m.RestartMihomo()
	case "generate":
		err = m.GenerateMihomoFile()
	case "download":
		path, err = m.DownloadMihomo()
	case "upgrade-ui":
		err = m.UpgradeMihomoUI()
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": path})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func handleAppQuit(app *AppRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		go app.RequestQuit()
	}
}

//go:embed ui.html
var embeddedIndexHTML string
