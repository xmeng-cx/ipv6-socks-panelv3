package panel

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed web/*
var webFiles embed.FS

const sessionCookieName = "ipv6_panel_session"
const sessionLifetime = 90 * 24 * time.Hour

type identityKey struct{}
type identity struct{ Username, Role string }
type HTTPServer struct{ manager *Manager }

func NewHTTPHandler(manager *Manager) http.Handler {
	s := &HTTPServer{manager: manager}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.HandleFunc("GET /api/v1/auth/me", s.me)
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/proxies", s.listProxies)
	mux.HandleFunc("POST /api/v1/proxies", s.addProxy)
	mux.HandleFunc("POST /api/v1/proxies/rotate-all", s.rotateAll)
	mux.HandleFunc("PUT /api/v1/network", s.updateNetwork)
	mux.HandleFunc("PUT /api/v1/network/prefix", s.updatePrefix)
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.getJob)
	mux.HandleFunc("DELETE /api/v1/proxies/{id}", s.deleteProxy)
	mux.HandleFunc("POST /api/v1/proxies/{id}/rotate", s.rotateProxy)
	mux.HandleFunc("GET /api/v1/users", s.listUsers)
	mux.HandleFunc("POST /api/v1/users", s.addUser)
	mux.HandleFunc("DELETE /api/v1/users/{username}", s.deleteUser)
	root, _ := fs.Sub(webFiles, "web")
	mux.Handle("/", spaHandler{files: http.FS(root)})
	return securityHeaders(s.sessionAuth(mux))
}

func currentIdentity(r *http.Request) identity {
	value, _ := r.Context().Value(identityKey{}).(identity)
	return value
}

func (s *HTTPServer) sessionAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		public := r.URL.Path == "/healthz" || r.URL.Path == "/api/v1/auth/login" || r.URL.Path == "/login.html" || r.URL.Path == "/login.js" || strings.HasSuffix(r.URL.Path, ".css")
		if public {
			if r.URL.Path == "/login.html" {
				if _, ok := s.sessionUser(r); ok {
					http.Redirect(w, r, "/", http.StatusSeeOther)
					return
				}
			}
			next.ServeHTTP(w, r)
			return
		}
		user, ok := s.sessionUser(r)
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeAPIError(w, http.StatusUnauthorized, "unauthorized", "登录已过期，请重新登录", nil)
			} else {
				http.Redirect(w, r, "/login.html", http.StatusSeeOther)
			}
			return
		}
		ctx := context.WithValue(r.Context(), identityKey{}, identity{Username: user.Username, Role: user.Role})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *HTTPServer) healthz(w http.ResponseWriter, _ *http.Request) {
	if healthy, _ := s.manager.Healthy(); !healthy {
		http.Error(w, "unhealthy", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *HTTPServer) sessionUser(r *http.Request) (User, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return User{}, false
	}
	username, ok := parseSession(s.manager.SessionSecret(), cookie.Value, time.Now())
	if !ok {
		return User{}, false
	}
	return s.manager.User(username)
}

func (s *HTTPServer) login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	user, ok := s.manager.Authenticate(request.Username, request.Password)
	if !ok {
		time.Sleep(250 * time.Millisecond)
		writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误", nil)
		return
	}
	expires := time.Now().Add(sessionLifetime)
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: signSession(s.manager.SessionSecret(), user.Username, expires), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, Expires: expires, MaxAge: int(sessionLifetime.Seconds())})
	writeJSON(w, http.StatusOK, map[string]any{"username": user.Username, "role": user.Role, "expiresAt": expires})
}

func (s *HTTPServer) logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *HTTPServer) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentIdentity(r))
}

func (s *HTTPServer) health(w http.ResponseWriter, r *http.Request) {
	user := currentIdentity(r)
	healthy, message := s.manager.Healthy()
	cfg := s.manager.Config()
	proxies := s.manager.ListForUser(user.Username)
	status := http.StatusOK
	if !healthy {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"status": map[bool]string{true: "healthy", false: "unhealthy"}[healthy], "message": message, "xrayRunning": s.manager.xray.Running(),
		"network": s.manager.NetworkInfo(), "proxyCount": len(proxies), "totalProxyCount": len(s.manager.List()), "initialProxies": cfg.InitialProxies,
		"maxProxies": cfg.MaxProxies, "basePort": cfg.BasePort, "username": user.Username, "role": user.Role, "isAdmin": user.Role == "admin",
		"advertiseHost": cfg.AdvertiseHost, "udp": cfg.SocksUDP, "hy2ObfsPassword": cfg.HY2ObfsPassword,
	})
}

func (s *HTTPServer) listProxies(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"proxies": s.manager.ListForUser(currentIdentity(r).Username)})
}

func (s *HTTPServer) addProxy(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Port     *int   `json:"port"`
		Protocol string `json:"protocol"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	proxy, err := s.manager.AddWithProtocolForUser(r.Context(), request.Port, request.Protocol, currentIdentity(r).Username)
	if err != nil {
		s.managerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, proxy)
}

func (s *HTTPServer) deleteProxy(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.DeleteForUser(r.Context(), currentIdentity(r).Username, r.PathValue("id")); err != nil {
		s.managerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *HTTPServer) rotateProxy(w http.ResponseWriter, r *http.Request) {
	proxy, err := s.manager.RotateForUser(r.Context(), currentIdentity(r).Username, r.PathValue("id"))
	if err != nil {
		s.managerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proxy)
}
func (s *HTTPServer) rotateAll(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusAccepted, s.manager.RotateAllForUser(currentIdentity(r).Username))
}
func (s *HTTPServer) getJob(w http.ResponseWriter, r *http.Request) {
	job, ok := s.manager.JobForUser(currentIdentity(r).Username, r.PathValue("id"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "job_not_found", "任务不存在", nil)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if currentIdentity(r).Role != "admin" {
		writeAPIError(w, http.StatusForbidden, "forbidden", "仅管理员可以执行此操作", nil)
		return false
	}
	return true
}

func (s *HTTPServer) updatePrefix(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var request struct {
		Prefix string `json:"prefix"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	info, err := s.manager.UpdatePrefix(r.Context(), request.Prefix)
	if err != nil {
		s.managerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"network": info})
}
func (s *HTTPServer) updateNetwork(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var request struct {
		Interface string `json:"interface"`
		Prefix    string `json:"prefix"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	info, err := s.manager.UpdateNetwork(r.Context(), request.Interface, request.Prefix)
	if err != nil {
		s.managerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"network": info})
}

func (s *HTTPServer) listUsers(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": s.manager.Users()})
}
func (s *HTTPServer) addUser(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	user, err := s.manager.AddUser(request.Username, request.Password)
	if err != nil {
		s.managerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}
func (s *HTTPServer) deleteUser(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if err := s.manager.DeleteUser(r.Context(), r.PathValue("username")); err != nil {
		s.managerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *HTTPServer) managerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrBusy):
		writeAPIError(w, http.StatusConflict, "proxy_busy", "线路正在执行其他操作", nil)
	case errors.Is(err, ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "对象不存在", nil)
	case errors.Is(err, ErrLimit):
		writeAPIError(w, http.StatusConflict, "proxy_limit", "已达到线路数量上限", nil)
	case errors.Is(err, ErrForbidden):
		writeAPIError(w, http.StatusForbidden, "forbidden", "不允许执行此操作", nil)
	case errors.Is(err, ErrUserExists):
		writeAPIError(w, http.StatusConflict, "user_exists", "用户名已存在", nil)
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
