package config

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Env           string
	HTTPAddr      string
	Store         string
	DatabaseURL   string
	AdminKey      string
	ReadKey       string
	PollEnabled   bool
	SSEMaxClients int
	CORSOrigins   []string
}

func Load() (Config, error) { return FromEnv(os.Getenv) }
func FromEnv(get func(string) string) (c Config, err error) {
	value := func(k, d string) string {
		if v := get(k); v != "" {
			return v
		}
		return d
	}
	c = Config{Env: value("APP_ENV", "development"), HTTPAddr: value("HTTP_ADDR", "127.0.0.1:8091"), Store: value("STORE", "postgres"), DatabaseURL: get("DATABASE_URL"), AdminKey: get("ADMIN_API_KEY"), ReadKey: get("READ_API_KEY"), SSEMaxClients: 128}
	if c.Env != "development" && c.Env != "test" && c.Env != "production" {
		return c, errors.New("APP_ENV must be development, test or production")
	}
	if len(c.AdminKey) < 32 || len(c.AdminKey) > 256 || len(c.ReadKey) < 32 || len(c.ReadKey) > 256 || c.AdminKey == c.ReadKey || strings.TrimSpace(c.AdminKey) != c.AdminKey || strings.TrimSpace(c.ReadKey) != c.ReadKey {
		return c, errors.New("ADMIN_API_KEY and READ_API_KEY must be distinct 32-256 character secrets without surrounding whitespace")
	}
	if c.Store != "postgres" && c.Store != "memory" {
		return c, errors.New("STORE must be postgres or memory")
	}
	if c.Store == "postgres" && c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required for postgres")
	}
	if c.Env == "production" && c.Store == "memory" {
		return c, errors.New("production does not allow the volatile memory store")
	}
	c.PollEnabled, err = strconv.ParseBool(value("POLL_ENABLED", "true"))
	if err != nil {
		return c, errors.New("invalid POLL_ENABLED")
	}
	c.SSEMaxClients, err = strconv.Atoi(value("SSE_MAX_CLIENTS", "128"))
	if err != nil || c.SSEMaxClients < 1 || c.SSEMaxClients > 10000 {
		return c, errors.New("SSE_MAX_CLIENTS must be 1-10000")
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
