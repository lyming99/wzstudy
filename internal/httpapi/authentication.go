package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"wzstudy/internal/auth"
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
	var token string
	if r.Header.Get("Authorization") != "" {
		token = bearer(r)
		// Explicit API credentials retain precedence over all browser cookies.
		if study.EqualKey(study.HashKey(s.cfg.AdminKey), token) {
			return true
		}
	} else if cookie, err := r.Cookie(userSessionCookie); err == nil {
		if len(cookie.Value) > 100 {
			return false
		}
		token = cookie.Value
	} else {
		// Only legacy in-memory administrator sessions can use the old cookie;
		// the new login never issues one and a restart invalidates them.
		return s.sessionValid(r)
	}
	if !strings.HasPrefix(token, "wzus_") {
		return false
	}
	administrator, err := s.service.AdministratorForUserSession(r.Context(), token)
	if err != nil {
		return false
	}
	if expected := r.Header.Get("X-WzStudy-User"); expected != "" && expected != administrator.ID {
		return false
	}
	if s.cfg.SystemAdminEmail != "" {
		configured, err := auth.NormalizeEmail(s.cfg.SystemAdminEmail)
		if err != nil || configured != administrator.Email {
			return false
		}
	}
	return true
}

func (s *Server) revokeAdminCookie(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return
	}
	if len(cookie.Value) <= 100 {
		sum := sha256.Sum256([]byte(cookie.Value))
		s.authMu.Lock()
		delete(s.sessions, hex.EncodeToString(sum[:]))
		s.authMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.cfg.Env == "production" || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
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
	s.issueUserLogin(w, r, in.Email, in.Password, "", true)
	in.Password = ""
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	credentialRequest := userCredentialRequest(r)
	token := bearer(credentialRequest)
	if strings.HasPrefix(token, "wzus_") {
		if err := s.service.LogoutUser(r.Context(), token); err != nil && !errors.Is(err, study.ErrUnauthorized) && !errors.Is(err, study.ErrNotFound) {
			s.fail(w, err)
			return
		}
		if r.Header.Get("Authorization") == "" {
			s.clearUserCookie(w, r)
		}
	}
	if r.Header.Get("Authorization") == "" {
		s.revokeAdminCookie(w, r)
	}
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
	success(w, 200, map[string]any{"authenticated": true, "role": "admin", "version": buildinfo.Version, "max_upload_bytes": s.cfg.MaxUploadBytes, "store": s.cfg.Store, "administrator": administrator, "authentication_method": method})
}
