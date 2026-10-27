package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"wzstudy/internal/adminui"
	"wzstudy/internal/buildinfo"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"wzstudy/docs"
	"wzstudy/internal/config"
	"wzstudy/internal/feed"
	"wzstudy/internal/study"
)

type Server struct {
	loginBuckets  map[string]loginBucket
	passwordSlots chan struct{}
	authMu        sync.Mutex
	sessions      map[string]time.Time
	cfg           config.Config
	service       *study.Service
	worker        *feed.Worker
	logger        *slog.Logger
}

func New(cfg config.Config, service *study.Service, worker *feed.Worker, logger *slog.Logger) http.Handler {
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}
	if cfg.MaxUploadBytes <= 0 {
		cfg.MaxUploadBytes = 200 << 20
	}
	s := &Server{loginBuckets: map[string]loginBucket{}, passwordSlots: make(chan struct{}, 2), sessions: map[string]time.Time{}, cfg: cfg, service: service, worker: worker, logger: logger}
	r := chi.NewRouter()
	r.Use(s.middleware)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { failure(w, 404, "not_found", "接口不存在") })
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		failure(w, 405, "method_not_allowed", "不支持该请求方法")
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		success(w, 200, map[string]any{"service": "wzstudy", "version": buildinfo.Version, "admin": "/admin/", "api": "/api/v1", "docs": "/docs", "openapi": "/openapi.json"})
	})
	r.Get("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusTemporaryRedirect)
	})
	r.Get("/admin/*", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/admin/")
		if name == "" {
			name = "index.html"
		}
		if name != "index.html" && name != "app.js" && name != "app.css" && name != "qrcodegen.js" && name != "roadmaps.js" && name != "roadmaps.css" {
			http.NotFound(w, r)
			return
		}
		b, e := adminui.Files.ReadFile(name)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' blob:; media-src 'self' blob:; connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		typ := "text/html; charset=utf-8"
		if name == "app.js" || name == "qrcodegen.js" || name == "roadmaps.js" {
			typ = "text/javascript; charset=utf-8"
		} else if name == "app.css" || name == "roadmaps.css" {
			typ = "text/css; charset=utf-8"
		}
		w.Header().Set("Content-Type", typ)
		_, _ = w.Write(b)
	})
	r.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
		b, _ := docs.Files.ReadFile("API.md")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(b)
	})
	r.Get("/docs/management", func(w http.ResponseWriter, r *http.Request) {
		b, _ := docs.Files.ReadFile("MANAGEMENT.md")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(b)
	})
	r.Get("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		b, _ := docs.Files.ReadFile("openapi.json")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(b)
	})
	r.Get("/api/v1/health/live", func(w http.ResponseWriter, r *http.Request) { success(w, 200, map[string]string{"status": "ok"}) })
	r.Get("/api/v1/health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if service.Store.Ping(ctx) != nil {
			failure(w, 503, "not_ready", "数据存储尚未就绪")
			return
		}
		success(w, 200, map[string]string{"status": "ready"})
	})
	r.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(s.require(false))
			r.Get("/media/{id}", s.media)
			r.Get("/sources", s.publicSources)
			r.Get("/items", s.publicItems)
			r.Get("/items/{id}", s.publicItem)
			r.Get("/items/{id}/learning-stats", s.learningItemStats)
			r.Get("/catalog/collections", s.publicCatalogCollections)
			r.Get("/catalog/collections/{id}", s.publicCatalogCollection)
		})
		r.With(s.requirePermission(study.PermissionSync)).Get("/changes", s.changes)
		r.With(s.requirePermission(study.PermissionSync)).Get("/catalog/collections/changes", s.catalogChanges)
		r.Post("/auth/login", s.login)
		r.With(s.require(true)).Get("/auth/session", s.session)
		r.With(s.require(true)).Post("/auth/logout", s.logout)
		r.Get("/client/connection", s.clientConnection)
		r.Post("/client/feedback", s.reportFeedback)
		r.Post("/client/feedback/import-legacy", s.importLegacyFeedback)
		r.Get("/client/feedback", s.clientFeedback)
		r.Get("/client/feedback/events", s.clientFeedbackEvents)
		r.Get("/client/recommendations", s.clientRecommendations)
		r.Get("/client/learning/capabilities", s.learningCapabilities)
		r.Get("/client/learning", s.clientLearning)
		r.Post("/client/learning/events", s.reportLearning)
		r.Post("/client/learning/import-legacy-records", s.importLegacyRecords)
		r.Post("/client/learning/import-legacy-reviews", s.importLegacyReviews)
		r.Post("/client/learning/drafts/commands", s.learningDraftCommand)
		r.Post("/client/learning/drafts/import-legacy", s.importLegacyLearningDraft)
		r.Get("/client/learning/items/{item_id}/draft", s.learningDraft)
		r.Get("/client/learning/events", s.clientLearningEvents)
		r.Get("/client/learning/items/{item_id}/records", s.clientLearningRecords)
		r.Get("/client/learning/items/{item_id}/records/{record_id}", s.clientLearningRecord)
		r.Get("/client/learning/items/{item_id}/stats", s.clientLearningItemStats)
		r.Get("/client/learning/collections", s.personalCollections)
		r.Get("/client/learning/collections/{collection_id}", s.personalCollection)
		r.Post("/client/learning/collections/commands", s.personalCollectionCommand)
		r.Post("/client/learning/collections/import-legacy", s.importLegacyPersonalCollection)
		r.Get("/ingest/{id}/status", s.ingestStatus)
		r.Post("/ingest/{id}/files", s.upload)
		r.Post("/ingest/{id}", s.ingest)
		r.Route("/admin", func(r chi.Router) {
			r.Use(s.require(true))
			r.Get("/workspace", s.workspace)
			r.Get("/roadmaps", s.learningRoadmaps)
			r.Post("/roadmaps", s.saveLearningRoadmap)
			r.Get("/roadmaps/{id}", s.learningRoadmap)
			r.Put("/roadmaps/{id}", s.saveLearningRoadmap)
			r.Post("/roadmaps/{id}/delete", s.deleteLearningRoadmap)
			r.Get("/catalog/collections", s.adminCatalogCollections)
			r.Post("/catalog/collections", s.saveCatalogCollection)
			r.Get("/catalog/collections/{id}", s.adminCatalogCollection)
			r.Put("/catalog/collections/{id}", s.saveCatalogCollection)
			r.Post("/catalog/collections/{id}/publish", s.publishCatalogCollection)
			r.Post("/catalog/collections/{id}/unpublish", s.unpublishCatalogCollection)
			r.Post("/catalog/collections/{id}/delete", s.deleteCatalogCollection)
			r.Get("/clients", s.clients)
			r.Post("/clients", s.createClient)
			r.Get("/clients/{id}", s.client)
			r.Get("/clients/{id}/feedback", s.adminClientFeedback)
			r.Get("/clients/{id}/feedback/events", s.adminClientFeedbackEvents)
			r.Get("/clients/{id}/learning", s.adminClientLearning)
			r.Get("/clients/{id}/learning/events", s.adminClientLearningEvents)
			r.Get("/clients/{id}/learning/items/{item_id}/records", s.adminClientLearningRecords)
			r.Get("/clients/{id}/items", s.adminClientItems)
			r.Put("/clients/{id}", s.updateClient)
			r.Post("/clients/{id}/key", s.rotateClientKey)
			r.Get("/identity-migration", s.previewIdentityMigration)
			r.Post("/identity-migration", s.migrateIdentities)
			r.Get("/intakes", s.intakeHistory)
			r.Put("/settings", s.saveSettings)
			r.Post("/uploads", s.upload)
			r.Post("/items", s.saveItem)
			r.Put("/items/{id}", s.saveItem)
			r.Post("/items/{id}/submit", s.submitItem)
			r.Post("/items/{id}/delete", s.deleteItem)
			r.Get("/sources", s.sources)
			r.Post("/sources", s.createSource)
			r.Get("/sources/{id}", s.source)
			r.Put("/sources/{id}", s.updateSource)
			r.Post("/sources/{id}/key", s.rotateKey)
			r.Post("/sources/{id}/refresh", s.refresh)
			r.Get("/items", s.adminItems)
			r.Get("/items/{id}", s.adminItem)
			r.Post("/items/{id}/publish", s.publish)
			r.Post("/items/{id}/unpublish", s.unpublish)
		})
	})
	return r
}
func success(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"data": data})
}
func failure(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, study.ErrInvalid):
		failure(w, 400, "invalid_request", "参数无效，请核对字段、游标、大小或来源类型")
	case errors.Is(err, study.ErrNotFound):
		failure(w, 404, "not_found", "记录不存在")
	case errors.Is(err, study.ErrUnauthorized):
		failure(w, 401, "unauthorized", "数据源密钥无效")
	case errors.Is(err, study.ErrConflict):
		failure(w, 409, "conflict", "版本冲突或记录已被修改，请刷新后重试")
	case errors.Is(err, study.ErrDisabled):
		failure(w, 403, "source_disabled", "客户端或数据源已停用接入")
	case errors.Is(err, study.ErrForbidden):
		failure(w, 403, "permission_denied", "当前客户端未获授此操作权限，请联系管理员")
	case errors.Is(err, study.ErrBusy):
		failure(w, 409, "source_busy", "该数据源正在采集或尚未到采集时间")
	case errors.Is(err, context.DeadlineExceeded):
		failure(w, 504, "timeout", "处理超时，请查询当前状态后再决定是否重试")
	default:
		s.logger.Error("request operation failed")
		failure(w, 500, "internal_error", "服务暂时不可用")
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeLimit(w, r, v, 4<<20)
}
func decodeLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typ != "application/json" {
		failure(w, 415, "unsupported_media_type", "需要 application/json 请求体")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	raw, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(readErr, &tooLarge) {
			failure(w, 413, "payload_too_large", "请求体超过接口大小限制")
		} else {
			failure(w, 400, "invalid_json", "无法读取 JSON 请求体")
		}
		return false
	}
	raw = bytes.TrimSpace(raw)
	if !utf8.Valid(raw) || len(raw) == 0 || raw[0] != '{' {
		failure(w, 400, "invalid_json", "请求体必须为 UTF-8 JSON 对象")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(v)
	if err == nil {
		var rest any
		if e := decoder.Decode(&rest); e != io.EOF {
			if e == nil {
				e = study.ErrInvalid
			}
			err = e
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			failure(w, 413, "payload_too_large", "请求体超过接口大小限制")
		} else {
			failure(w, 400, "invalid_json", "JSON 无效、包含未知字段或多段正文")
		}
		return false
	}
	return true
}
func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if len(v) < 7 || !strings.EqualFold(v[:7], "Bearer ") {
		return ""
	}
	key := v[7:]
	if strings.ContainsAny(key, " \t\r\n") || len(key) > 256 {
		return ""
	}
	return key
}
func (s *Server) require(admin bool) func(http.Handler) http.Handler {
	if !admin {
		return s.requirePermission(study.PermissionRead)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := bearer(r)
			valid := s.isAdmin(r)
			if !valid {
				w.Header().Set("WWW-Authenticate", "Bearer")
				failure(w, 401, "unauthorized", "需要有效的访问密钥")
				return
			}
			if key == "" && r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && r.Header.Get("Origin") == "" {
				failure(w, 403, "csrf_denied", "浏览器会话写入请求必须来自受信任页面")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(n int) {
	if w.status == 0 {
		w.status = n
		w.ResponseWriter.WriteHeader(n)
	}
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		id := uuid.NewString()
		sw.Header().Set("X-Request-ID", id)
		sw.Header().Set("X-Content-Type-Options", "nosniff")
		sw.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		sw.Header().Set("Cache-Control", "no-store")
		sw.Header().Set("Referrer-Policy", "no-referrer")
		defer func() {
			if recover() != nil {
				s.logger.Error("request panic", "request_id", id)
				if sw.status == 0 {
					failure(sw, 500, "internal_error", "服务暂时不可用")
				}
			}
			s.logger.Info("http request", "request_id", id, "method", r.Method, "path", r.URL.Path, "status", sw.status, "duration_ms", time.Since(start).Milliseconds())
		}()
		origin := r.Header.Get("Origin")
		if origin != "" {
			allowed := s.sameOrigin(r, origin)
			for _, v := range s.cfg.CORSOrigins {
				if v == origin {
					allowed = true
				}
			}
			if !allowed {
				failure(sw, 403, "origin_denied", "该跨域来源未授权")
				return
			}
			sw.Header().Set("Access-Control-Allow-Origin", origin)
			sw.Header().Set("Vary", "Origin")
			sw.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
		}
		if r.Method == http.MethodOptions {
			if origin == "" {
				failure(sw, 400, "invalid_request", "预检缺少 Origin")
				return
			}
			sw.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			sw.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			sw.WriteHeader(204)
			return
		}
		timeout := 30 * time.Second
		if strings.HasSuffix(r.URL.Path, "/files") || r.URL.Path == "/api/v1/admin/uploads" {
			timeout = 5 * time.Minute
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		r = r.WithContext(ctx)
		next.ServeHTTP(sw, r)
	})
}
func parseInt(raw string, defaultValue int) (int, error) {
	if raw == "" {
		return defaultValue, nil
	}
	v, e := strconv.Atoi(raw)
	if e != nil {
		return 0, study.ErrInvalid
	}
	return v, nil
}
func filter(r *http.Request, admin bool) (study.Filter, error) {
	q := r.URL.Query()
	limit, e := parseInt(q.Get("limit"), 20)
	if e != nil {
		return study.Filter{}, e
	}
	offset, e := parseInt(q.Get("offset"), 0)
	if e != nil {
		return study.Filter{}, e
	}
	ids, e := sourceIDs(q.Get("source_id"))
	if e != nil {
		return study.Filter{}, e
	}
	kind, status := q.Get("kind"), q.Get("status")
	if kind != "" && kind != "article" && kind != "audio" && kind != "video" && kind != "carousel" {
		return study.Filter{}, study.ErrInvalid
	}
	if status != "" && (!admin || (status != "draft" && status != "pending" && status != "published")) {
		return study.Filter{}, study.ErrInvalid
	}
	if sort := q.Get("sort"); sort != "" && sort != "asc" && sort != "desc" {
		return study.Filter{}, study.ErrInvalid
	}
	return study.Filter{Kind: kind, Status: status, Ascending: q.Get("sort") == "asc", SourceIDs: ids, Category: q.Get("category"), Tag: q.Get("tag"), Query: q.Get("q"), Limit: limit, Offset: offset, Admin: admin}, nil
}
func sourceIDs(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	ids := strings.Split(raw, ",")
	if len(ids) > 20 {
		return nil, study.ErrInvalid
	}
	for i, id := range ids {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return nil, study.ErrInvalid
		}
		ids[i] = parsed.String()
	}
	return ids, nil
}
