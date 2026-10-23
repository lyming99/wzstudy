package config

import (
	"strings"
	"testing"
)

func TestConfig(t *testing.T) {
	base := map[string]string{"STORE": "memory", "ADMIN_API_KEY": strings.Repeat("a", 40), "READ_API_KEY": strings.Repeat("r", 40)}
	load := func(m map[string]string) (Config, error) { return FromEnv(func(k string) string { return m[k] }) }
	if c, e := load(base); e != nil || c.HTTPAddr != "127.0.0.1:8091" || !c.PollEnabled {
		t.Fatal(c, e)
	}
	for _, test := range []struct{ k, v string }{{"APP_ENV", "production"}, {"ADMIN_API_KEY", "short"}, {"READ_API_KEY", base["ADMIN_API_KEY"]}, {"POLL_ENABLED", "maybe"}, {"SSE_MAX_CLIENTS", "0"}, {"CORS_ORIGINS", "*"}, {"CORS_ORIGINS", "https://example.com/"}, {"STORE", "sqlite"}} {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		m[test.k] = test.v
		if _, e := load(m); e == nil {
			t.Fatalf("accepted invalid %s=%s", test.k, test.v)
		}
	}
	base["CORS_ORIGINS"] = "https://study.example.com,http://localhost:3000"
	if _, e := load(base); e != nil {
		t.Fatal(e)
	}
	base["STORE"] = "postgres"
	if _, e := load(base); e == nil {
		t.Fatal("database URL not required")
	}
}
