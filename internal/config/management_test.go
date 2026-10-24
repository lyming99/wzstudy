package config

import (
	"strings"
	"testing"
)

func TestManagementStorageAndUploadLimits(t *testing.T) {
	base := map[string]string{"STORE": "file", "ADMIN_API_KEY": strings.Repeat("a", 40), "READ_API_KEY": strings.Repeat("r", 40)}
	load := func(values map[string]string) (Config, error) {
		return FromEnv(func(k string) string { return values[k] })
	}
	c, e := load(base)
	if e != nil || c.DataDir != "data" || c.MaxUploadBytes != 200<<20 {
		t.Fatal(c, e)
	}
	for _, value := range []string{"0", "-1", "1025", "invalid"} {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		m["UPLOAD_MAX_MB"] = value
		if _, e = load(m); e == nil {
			t.Fatal("accepted invalid upload limit", value)
		}
	}
	base["UPLOAD_MAX_MB"] = "1"
	base["DATA_DIR"] = "custom-data"
	if c, e = load(base); e != nil || c.MaxUploadBytes != 1<<20 || c.DataDir != "custom-data" {
		t.Fatal(c, e)
	}
	base["APP_ENV"] = "production"
	if _, e = load(base); e == nil {
		t.Fatal("production accepted single-process file storage")
	}
}
