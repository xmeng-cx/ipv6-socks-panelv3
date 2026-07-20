package panel

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed web/*
var webFiles embed.FS

type HTTPServer struct{ manager *Manager }

func NewHTTPHandler(manager *Manager) http.Handler {
	s := &HTTPServer{manager: manager}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/proxies", s.listProxies)
	mux.HandleFunc("POST /api/v1/proxies", s.addProxy)
	mux.HandleFunc("POST /api/v1/proxies/rotate-all", s.rotateAll)
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.getJob)
	mux.HandleFunc("DELETE /api/v1/proxies/{id}", s.deleteProxy)
	mux.HandleFunc("POST /api/v1/proxies/{id}/rotate", s.rotateProxy)
	root, _ := fs.Sub(webFiles, "web")
	mux.Handle("/", spaHandler{files: http.FS(root)})
	return securityHeaders(mux)
}

func (s *HTTPServer) health(w http.ResponseWriter, _ *http.Request) {
	healthy, message := s.manager.Healthy()
	cfg := s.manager.Config()
	status := http.StatusOK
	if !healthy {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"status":  map[bool]string{true: "healthy", false: "unhealthy"}[healthy],
		"message": message, "xrayRunning": s.manager.xray.Running(),
		"network": s.manager.NetworkInfo(), "proxyCount": len(s.manager.List()),
		"initialProxies": cfg.InitialProxies, "maxProxies": cfg.MaxProxies, "basePort": cfg.BasePort,
		"username": cfg.SocksUsername, "password": cfg.SocksPassword,
		"advertiseHost": cfg.AdvertiseHost, "udp": cfg.SocksUDP,
	})
}

func (s *HTTPServer) listProxies(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"proxies": s.manager.List()})
}

func (s *HTTPServer) addProxy(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Port *int `json:"port"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	proxy, err := s.manager.Add(r.Context(), request.Port)
	if err != nil {
		s.managerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, proxy)
}

func (s *HTTPServer) deleteProxy(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.managerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *HTTPServer) rotateProxy(w http.ResponseWriter, r *http.Request) {
	proxy, err := s.manager.Rotate(r.Context(), r.PathValue("id"))
	if err != nil {
		s.managerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proxy)
}

func (s *HTTPServer) rotateAll(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusAccepted, s.manager.RotateAll())
}

func (s *HTTPServer) getJob(w http.ResponseWriter, r *http.Request) {
	job, ok := s.manager.Job(r.PathValue("id"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "job_not_found", "任务不存在", nil)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *HTTPServer) managerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrBusy):
		writeAPIError(w, http.StatusConflict, "proxy_busy", "线路正在执行其他操作", nil)
	case errors.Is(err, ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "proxy_not_found", "线路不存在", nil)
	case errors.Is(err, ErrLimit):
		writeAPIError(w, http.StatusConflict, "proxy_limit", "已达到线路数量上限", nil)
	default:
		writeAPIError(w, http.StatusInternalServerError, "operation_failed", err.Error(), nil)
	}
}

func decodeJSON(r *http.Request, target any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "details": details}})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}

type spaHandler struct{ files http.FileSystem }

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	file, err := h.files.Open(path)
	if err != nil {
		r.URL.Path = "/"
		http.FileServer(h.files).ServeHTTP(w, r)
		return
	}
	_ = file.Close()
	http.FileServer(h.files).ServeHTTP(w, r)
}

var _ = fmt.Sprintf
