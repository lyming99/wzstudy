package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAdministratorConfigurationAliasesAndRawPassword(t *testing.T) {
	values := map[string]string{"APP_ENV": "test", "STORE": "memory", "ADMIN_API_KEY": strings.Repeat("a", 40), "READ_API_KEY": strings.Repeat("r", 40), "SYSTEM_ADMIN_EMAIL": "  Admin@Example.COM  ", "SYSTEM_ADMIN_PASSWORD": "  keep leading and trailing spaces  ", "SYSTEM_ADMIN_DISPLAY_NAME": " 系统管理员 "}
	get := func(key string) string { return values[key] }
	c, err := FromEnv(get)
	if err != nil || c.SystemAdminEmail != "admin@example.com" || c.SystemAdminPassword != values["SYSTEM_ADMIN_PASSWORD"] || c.SystemAdminDisplayName != "系统管理员" {
		t.Fatal("administrator configuration normalization failed", err)
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), values["SYSTEM_ADMIN_PASSWORD"]) {
		t.Fatal("config JSON exposed administrator password")
	}
	delete(values, "SYSTEM_ADMIN_EMAIL")
	delete(values, "SYSTEM_ADMIN_PASSWORD")
	delete(values, "SYSTEM_ADMIN_DISPLAY_NAME")
	values["BOOTSTRAP_ADMIN_EMAIL"] = "legacy@example.com"
	values["BOOTSTRAP_ADMIN_PASSWORD"] = "legacy-password"
	values["BOOTSTRAP_ADMIN_USERNAME"] = "Legacy administrator"
	c, err = FromEnv(get)
	if err != nil || c.SystemAdminEmail != "legacy@example.com" || c.SystemAdminDisplayName != "Legacy administrator" {
		t.Fatal(err)
	}
	for _, key := range []string{"BOOTSTRAP_ADMIN_EMAIL", "BOOTSTRAP_ADMIN_PASSWORD", "BOOTSTRAP_ADMIN_USERNAME"} {
		delete(values, key)
	}
	for key, value := range map[string]string{"SYSTEM_ADMIN_EMAIL": "Name <admin@example.com>", "SYSTEM_ADMIN_PASSWORD": "short", "SYSTEM_ADMIN_DISPLAY_NAME": "header\r\ninjection"} {
		values[key] = value
		if _, err = FromEnv(get); err == nil {
			t.Fatal("invalid admin setting accepted", key)
		}
		delete(values, key)
	}
}
