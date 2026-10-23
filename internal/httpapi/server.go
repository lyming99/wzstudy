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
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"wzstudy/docs"
	"wzstudy/internal/config"
	"wzstudy/internal/feed"
	"wzstudy/internal/study"
)

type Server struct {
	cfg     config.Config
	service *study.Service
	worker  *feed.Worker
	logger  *slog.Logger
	streams chan struct{}
}

func New(cfg config.Config, service *study.Service, worker *feed.Worker, logger *slog.Logger) http.Handler {
	if cfg.SSEMaxClients < 1 {
		cfg.SSEMaxClients = 128
	}
	s := &Server{cfg: cfg, service: service, worker: worker, logger: logger, streams: make(chan struct{}, cfg.SSEMaxClients)}
	r := chi.NewRouter()
	r.Use(s.middleware)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { failure(w, 404, "not_found", "接口不存在") })
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		failure(w, 405, "method_not_allowed", "不支持该请求方法")
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		success(w, 200, map[string]any{"service": "wzstudy", "version": "0.1.0", "api": "/api/v1", "docs": "/docs", "openapi": "/openapi.json"})
	})
	r.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
		b, _ := docs.Files.ReadFile("API.md")
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
			r.Get("/sources", s.publicSources)
			r.Get("/items", s.publicItems)
			r.Get("/items/{id}", s.publicItem)
			r.Get("/changes", s.changes)
			r.Get("/stream", s.stream)
		})
		r.Post("/ingest/{id}", s.ingest)
		r.Route("/admin", func(r chi.Router) {
			r.Use(s.require(true))
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
		failure(w, 403, "source_disabled", "数据源已停用接入")
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
	typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typ != "application/json" {
		failure(w, 415, "unsupported_media_type", "需要 application/json 请求体")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	raw, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(readErr, &tooLarge) {
			failure(w, 413, "payload_too_large", "请求体超过 4 MiB")
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
			failure(w, 413, "payload_too_large", "请求体超过 4 MiB")
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
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := bearer(r)
			valid := study.EqualKey(study.HashKey(s.cfg.AdminKey), key)
			if !admin {
				valid = valid || study.EqualKey(study.HashKey(s.cfg.ReadKey), key)
			}
			if !valid {
				w.Header().Set("WWW-Authenticate", "Bearer")
				failure(w, 401, "unauthorized", "需要有效的访问密钥")
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
			allowed := false
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
			sw.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID")
			sw.WriteHeader(204)
			return
		}
		if r.URL.Path != "/api/v1/stream" {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
		}
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
	return study.Filter{SourceIDs: ids, Category: q.Get("category"), Tag: q.Get("tag"), Query: q.Get("q"), Limit: limit, Offset: offset, Admin: admin}, nil
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
