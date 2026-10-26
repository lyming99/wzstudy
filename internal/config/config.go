package config

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"wzstudy/internal/auth"
)

type Config struct {
	SystemAdminEmail       string
	SystemAdminPassword    string `json:"-"`
	SystemAdminDisplayName string
	DataDir                string
	MaxUploadBytes         int64
	Env                    string
	HTTPAddr               string
	Store                  string
	DatabaseURL            string
	AdminKey               string
	ReadKey                string
	PollEnabled            bool
	CORSOrigins            []string
}

func Load() (Config, error) { return FromEnv(os.Getenv) }
func FromEnv(get func(string) string) (c Config, err error) {
	value := func(k, d string) string {
		if v := get(k); v != "" {
			return v
		}
		return d
	}
	c = Config{Env: value("APP_ENV", "development"), HTTPAddr: value("HTTP_ADDR", "0.0.0.0:8091"), Store: value("STORE", "postgres"), DatabaseURL: get("DATABASE_URL"), AdminKey: get("ADMIN_API_KEY"), ReadKey: get("READ_API_KEY")}
	if c.Env != "development" && c.Env != "test" && c.Env != "production" {
		return c, errors.New("APP_ENV must be development, test or production")
	}
	if len(c.AdminKey) < 32 || len(c.AdminKey) > 256 || len(c.ReadKey) < 32 || len(c.ReadKey) > 256 || c.AdminKey == c.ReadKey || strings.TrimSpace(c.AdminKey) != c.AdminKey || strings.TrimSpace(c.ReadKey) != c.ReadKey {
		return c, errors.New("ADMIN_API_KEY and READ_API_KEY must be distinct 32-256 character secrets without surrounding whitespace")
	}
	if c.Store != "postgres" && c.Store != "memory" && c.Store != "file" {
		return c, errors.New("STORE must be postgres, file or memory")
	}
	c.DataDir = value("DATA_DIR", "data")
	uploadMB, e := strconv.ParseInt(value("UPLOAD_MAX_MB", "200"), 10, 64)
	if e != nil || uploadMB < 1 || uploadMB > 1024 {
		return c, errors.New("UPLOAD_MAX_MB must be 1-1024")
	}
	c.MaxUploadBytes = uploadMB << 20
	first := func(names ...string) string {
		for _, name := range names {
			if v := get(name); v != "" {
				return v
			}
		}
		return ""
	}
	firstTrimmed := func(names ...string) string {
		for _, name := range names {
			if value := strings.TrimSpace(get(name)); value != "" {
				return value
			}
		}
		return ""
	}
	c.SystemAdminEmail = firstTrimmed("SYSTEM_ADMIN_EMAIL", "BOOTSTRAP_ADMIN_EMAIL")
	if c.SystemAdminEmail != "" {
		if c.SystemAdminEmail, err = auth.NormalizeEmail(c.SystemAdminEmail); err != nil {
			return c, errors.New("SYSTEM_ADMIN_EMAIL must be a valid email address")
		}
	}
	c.SystemAdminPassword = first("SYSTEM_ADMIN_PASSWORD", "BOOTSTRAP_ADMIN_PASSWORD")
	if c.SystemAdminPassword != "" && auth.ValidatePassword(c.SystemAdminPassword) != nil {
		return c, errors.New("SYSTEM_ADMIN_PASSWORD must contain 8-128 UTF-8 bytes without NUL")
	}
	c.SystemAdminDisplayName = firstTrimmed("SYSTEM_ADMIN_DISPLAY_NAME", "SYSTEM_ADMIN_USERNAME", "BOOTSTRAP_ADMIN_DISPLAY_NAME", "BOOTSTRAP_ADMIN_USERNAME")
	if len(c.SystemAdminDisplayName) > 120 || strings.ContainsAny(c.SystemAdminDisplayName, "\r\n\x00") {
		return c, errors.New("SYSTEM_ADMIN_DISPLAY_NAME must contain at most 120 header-safe UTF-8 bytes")
	}
	if c.Store == "postgres" && c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required for postgres")
	}
	if c.Env == "production" && c.Store != "postgres" {
		return c, errors.New("production requires PostgreSQL; file and memory stores are development-only")
	}
	c.PollEnabled, err = strconv.ParseBool(value("POLL_ENABLED", "true"))
	if err != nil {
		return c, errors.New("invalid POLL_ENABLED")
	}
	for _, v := range strings.Split(get("CORS_ORIGINS"), ",") {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		u, e := url.Parse(v)
		if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return c, errors.New("CORS_ORIGINS must contain exact http(s) origins, no wildcard or path")
		}
		if c.Env == "production" && u.Scheme != "https" {
			return c, errors.New("production CORS origins must use https")
		}
		c.CORSOrigins = append(c.CORSOrigins, v)
	}
	return c, nil
}
