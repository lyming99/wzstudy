package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"wzstudy/internal/buildinfo"
	"wzstudy/internal/study"
)

const sessionCookie = "wzstudy_admin"

type loginBucket struct {
	Start time.Time
	Count int
}

func (s *Server) sessionValid(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || len(cookie.Value) > 100 {
		return false
	}
	sum := sha256.Sum256([]byte(cookie.Value))
	id := hex.EncodeToString(sum[:])
	s.authMu.Lock()
	defer s.authMu.Unlock()
	expiry, ok := s.sessions[id]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(s.sessions, id)
		return false
	}
	return true
}

func (s *Server) isAdmin(r *http.Request) bool {
	if key := bearer(r); key != "" {
		// API credentials remain supported; they are NOT a browser-login password.
		return study.EqualKey(study.HashKey(s.cfg.AdminKey), key)
	}
	return s.sessionValid(r)
}

// Deliberately ignore forwarded IP headers unless a future trusted-proxy
// policy is explicitly implemented. Bound the map and expensive hash work.
func (s *Server) allowLogin(r *http.Request) bool {
	now := time.Now()
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if ip == "" || len(ip) > 256 {
		ip = "unknown"
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if len(s.loginBuckets) >= 4096 {
		for key, bucket := range s.loginBuckets {
			if now.Sub(bucket.Start) >= time.Minute {
				delete(s.loginBuckets, key)
			}
		}
	}
	bucket, exists := s.loginBuckets[ip]
	if !exists && len(s.loginBuckets) >= 4096 {
		return false
	}
	if !exists || now.Sub(bucket.Start) >= time.Minute {
		bucket = loginBucket{Start: now}
	}
	if bucket.Count >= 10 {
		return false
	}
	bucket.Count++
	s.loginBuckets[ip] = bucket
	return true
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.allowLogin(r) {
		w.Header().Set("Retry-After", "60")
		failure(w, 429, "login_rate_limited", "登录尝试过于频繁，请一分钟后重试")
		return
	}
	select {
	case s.passwordSlots <- struct{}{}:
		defer func() { <-s.passwordSlots }()
	default:
		w.Header().Set("Retry-After", "1")
		failure(w, 429, "login_busy", "登录服务繁忙，请稍后重试")
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeLimit(w, r, &in, 4096) {
		return
	}
	administrator, err := s.service.AuthenticateAdministrator(r.Context(), in.Email, in.Password)
	in.Password = ""
	if err == study.ErrUnauthorized {
		failure(w, 401, "unauthorized", "邮箱或密码不正确")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	bytes := make([]byte, 32)
	if _, err = rand.Read(bytes); err != nil {
		s.fail(w, err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	sum := sha256.Sum256([]byte(token))
	s.authMu.Lock()
	now := time.Now()
	for id, expiry := range s.sessions {
		if now.After(expiry) {
			delete(s.sessions, id)
		}
	}
	if len(s.sessions) >= 1024 {
		s.authMu.Unlock()
		failure(w, 429, "session_limit", "会话数量已达上限，请稍后重试")
		return
	}
	s.sessions[hex.EncodeToString(sum[:])] = now.Add(8 * time.Hour)
	s.authMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.cfg.Env == "production" || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 3600})
	success(w, 200, map[string]any{"authenticated": true, "version": buildinfo.Version, "max_upload_bytes": s.cfg.MaxUploadBytes, "administrator": administrator})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		sum := sha256.Sum256([]byte(cookie.Value))
		s.authMu.Lock()
		delete(s.sessions, hex.EncodeToString(sum[:]))
		s.authMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.cfg.Env == "production" || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	success(w, 200, map[string]bool{"authenticated": false})
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	administrator, err := s.service.Administrator(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	method := "email_password"
	if strings.TrimSpace(bearer(r)) != "" {
		method = "api_key"
	}
	success(w, 200, map[string]any{"authenticated": true, "version": buildinfo.Version, "max_upload_bytes": s.cfg.MaxUploadBytes, "store": s.cfg.Store, "administrator": administrator, "authentication_method": method})
}
