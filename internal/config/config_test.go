package config

import (
	"strings"
	"testing"
)

func TestConfig(t *testing.T) {
	base := map[string]string{"STORE": "memory", "ADMIN_API_KEY": strings.Repeat("a", 40), "READ_API_KEY": strings.Repeat("r", 40)}
	load := func(m map[string]string) (Config, error) { return FromEnv(func(k string) string { return m[k] }) }
	if c, e := load(base); e != nil || c.HTTPAddr != "0.0.0.0:8091" || !c.PollEnabled {
		t.Fatal(c, e)
	}
	for _, address := range []string{"127.0.0.1:8091", "192.0.2.10:8092", "0.0.0.0:8092", "[::1]:8091"} {
		t.Run("HTTP_ADDR="+address, func(t *testing.T) {
			m := map[string]string{}
			for k, v := range base {
				m[k] = v
			}
			m["HTTP_ADDR"] = address
			if c, e := load(m); e != nil || c.HTTPAddr != address {
				t.Fatalf("HTTP_ADDR override not preserved: got %q, error %v", c.HTTPAddr, e)
			}
		})
	}
	for _, test := range []struct{ k, v string }{{"APP_ENV", "production"}, {"ADMIN_API_KEY", "short"}, {"READ_API_KEY", base["ADMIN_API_KEY"]}, {"POLL_ENABLED", "maybe"}, {"CORS_ORIGINS", "*"}, {"CORS_ORIGINS", "https://example.com/"}, {"STORE", "sqlite"}} {
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
