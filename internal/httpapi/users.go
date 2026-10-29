package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"wzstudy/internal/auth"
	"wzstudy/internal/buildinfo"
	"wzstudy/internal/study"
)

const userSessionCookie = "wzstudy_user"

// An explicit Authorization header always wins, including an invalid one. A
// user's browser cookie must never upgrade a source/client/admin API credential.
func userCredentialRequest(r *http.Request) *http.Request {
	if r.Header.Get("Authorization") != "" {
		return r
	}
	cookie, err := r.Cookie(userSessionCookie)
	if err != nil || len(cookie.Value) > 100 {
		return r
	}
	next := r.Clone(r.Context())
	next.Header.Set("Authorization", "Bearer "+cookie.Value)
	return next
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieAuth := r.Header.Get("Authorization") == ""
		r = userCredentialRequest(r)
		user, err := s.service.UserForSession(r.Context(), bearer(r))
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			if err == study.ErrUnauthorized {
				failure(w, 401, "user_login_required", "请先使用学习用户账号登录；客户端、管理员或录入密钥不能代替用户身份")
			} else {
				s.fail(w, err)
			}
			return
		}
		if expected := r.Header.Get("X-WzStudy-User"); expected != "" && expected != user.ID {
			failure(w, 401, "identity_changed", "用户账号已切换，请重新登录后操作")
			return
		}
		if cookieAuth && r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && !s.sameOrigin(r, r.Header.Get("Origin")) {
			failure(w, 403, "csrf_denied", "用户会话写入请求必须来自本站页面")
			return
		}
		if r.URL.Query().Has("user_id") || r.URL.Query().Has("owner_id") || r.URL.Query().Has("client_id") {
			s.fail(w, study.ErrInvalid)
			return
		}
		if cookieAuth {
			// Clean up legacy browsers that held both identities before the
			// shared workbench enforced exclusive browser sessions.
			s.revokeAdminCookie(w, r)
		}
		next.ServeHTTP(w, r.WithContext(study.WithWorkspace(r.Context(), user.ID)))
	})
}

func (s *Server) userLogin(w http.ResponseWriter, r *http.Request) {
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
		Email       string `json:"email"`
		Password    string `json:"password"`
		SessionMode string `json:"session_mode,omitempty"`
	}
	if !decodeLimit(w, r, &in, 4096) {
		return
	}
	if in.SessionMode != "" && in.SessionMode != "browser" && in.SessionMode != "token" {
		s.fail(w, study.ErrInvalid)
		return
	}
	s.issueUserLogin(w, r, in.Email, in.Password, in.SessionMode, false)
	in.Password = ""
}

// Both browser and native logins issue one personal-workspace session. An
// administrator capability is attached only to a verified administrator
// principal, never just to an independently authenticated same-email learner.
func (s *Server) issueUserLogin(w http.ResponseWriter, r *http.Request, email, password, sessionMode string, unified bool) {
	administrator, adminErr := s.service.Administrator(r.Context())
	if adminErr != nil && !errors.Is(adminErr, study.ErrNotFound) {
		s.fail(w, adminErr)
		return
	}
	if adminErr == nil && s.cfg.SystemAdminEmail != "" {
		configured, err := auth.NormalizeEmail(s.cfg.SystemAdminEmail)
		if err != nil || configured != administrator.Email {
			failure(w, 503, "administrator_configuration_mismatch", "管理员邮箱配置与已有账号不一致，请检查配置；未转移管理员权限")
			return
		}
	}
	var user study.User
	var token string
	var expires time.Time
	var err error
	normalized, normalizeErr := auth.NormalizeEmail(email)
	if adminErr == nil && normalizeErr == nil && normalized == administrator.Email {
		user, token, expires, err = s.service.LoginAdministratorUser(r.Context(), email, password)
		if errors.Is(err, study.ErrUnauthorized) {
			// A historical learner with the same email remains a distinct,
			// non-admin principal, with its own credentials and workspace.
			user, token, expires, err = s.service.LoginUser(r.Context(), email, password)
		}
	} else {
		user, token, expires, err = s.service.LoginUser(r.Context(), email, password)
	}
	password = ""
	if errors.Is(err, study.ErrUnauthorized) {
		message := "邮箱或密码不正确，或账号已停用"
		if unified {
			message = "邮箱或密码不正确"
		}
		failure(w, 401, "unauthorized", message)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	role := "user"
	verifiedAdmin, roleErr := s.service.AdministratorForUserSession(r.Context(), token)
	if roleErr == nil {
		role = "admin"
	} else if !errors.Is(roleErr, study.ErrUnauthorized) {
		_ = s.service.LogoutUser(r.Context(), token)
		s.fail(w, roleErr)
		return
	}
	out := map[string]any{"authenticated": true, "role": role, "user": user, "user_id": user.ID, "expires_at": expires, "max_upload_bytes": s.cfg.MaxUploadBytes, "store": s.cfg.Store}
	if role == "admin" {
		out["administrator"], out["version"] = verifiedAdmin, buildinfo.Version
	}
	if sessionMode == "token" {
		out["access_token"], out["token_type"] = token, "Bearer"
	} else {
		// Only a successful login switches this browser. Other devices and
		// independent native tokens are not revoked.
		if old, cookieErr := r.Cookie(userSessionCookie); cookieErr == nil && len(old.Value) <= 100 && strings.HasPrefix(old.Value, "wzus_") {
			if logoutErr := s.service.LogoutUser(r.Context(), old.Value); logoutErr != nil && !errors.Is(logoutErr, study.ErrUnauthorized) && !errors.Is(logoutErr, study.ErrNotFound) {
				_ = s.service.LogoutUser(r.Context(), token)
				s.fail(w, logoutErr)
				return
			}
		}
		s.revokeAdminCookie(w, r)
		http.SetCookie(w, &http.Cookie{Name: userSessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.cfg.Env == "production" || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: int(study.UserSessionLifetime / time.Second)})
	}
	success(w, 200, out)
}

func (s *Server) userSession(w http.ResponseWriter, r *http.Request) {
	user, err := s.service.UserForSession(r.Context(), bearer(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"authenticated": true, "role": "user", "user": user, "user_id": user.ID, "max_upload_bytes": s.cfg.MaxUploadBytes, "store": s.cfg.Store}
	if s.isAdmin(r) {
		administrator, e := s.service.AdministratorForUserSession(r.Context(), bearer(r))
		if e != nil {
			s.fail(w, e)
			return
		}
		out["role"], out["administrator"] = "admin", administrator
	}
	success(w, 200, out)
}
func (s *Server) clearUserCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: userSessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.cfg.Env == "production" || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}
func (s *Server) userLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.service.LogoutUser(r.Context(), bearer(r)); err != nil {
		s.fail(w, err)
		return
	}
	s.clearUserCookie(w, r)
	success(w, 200, map[string]any{"authenticated": false})
}
func (s *Server) userPassword(w http.ResponseWriter, r *http.Request) {
	select {
	case s.passwordSlots <- struct{}{}:
		defer func() { <-s.passwordSlots }()
	default:
		failure(w, 429, "login_busy", "服务繁忙，请稍后重试")
		return
	}
	var in struct {
		Current  string `json:"current_password"`
		Password string `json:"password"`
	}
	if !decodeLimit(w, r, &in, 4096) {
		return
	}
	err := s.service.ChangeUserPassword(r.Context(), bearer(r), in.Current, in.Password)
	in.Current, in.Password = "", ""
	if err != nil {
		s.fail(w, err)
		return
	}
	s.clearUserCookie(w, r)
	success(w, 200, map[string]any{"authenticated": false, "reauthentication_required": true})
}
func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	out, err := s.service.Users(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in study.UserInput
	if !decodeLimit(w, r, &in, 4096) {
		return
	}
	select {
	case s.passwordSlots <- struct{}{}:
		defer func() { <-s.passwordSlots }()
	default:
		failure(w, 429, "login_busy", "服务繁忙，请稍后重试")
		return
	}
	out, err := s.service.CreateUser(r.Context(), in)
	in.Password = ""
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 201, out)
}
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
		study.UserInput
	}
	if !decodeLimit(w, r, &in, 4096) {
		return
	}
	select {
	case s.passwordSlots <- struct{}{}:
		defer func() { <-s.passwordSlots }()
	default:
		failure(w, 429, "login_busy", "服务繁忙，请稍后重试")
		return
	}
	out, err := s.service.UpdateUser(r.Context(), chi.URLParam(r, "id"), in.Revision, in.UserInput)
	in.Password = ""
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}

func (s *Server) requireManagedUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.service.User(r.Context(), chi.URLParam(r, "id")); err != nil {
			s.fail(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}
