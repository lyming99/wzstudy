package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashSaltAndWhitespace(t *testing.T) {
	password := "  keep leading and trailing spaces  "
	one, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	two, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if one == two || strings.Contains(one, password) || !strings.HasPrefix(one, "$argon2id$v1$m=65536,t=3,p=1$") {
		t.Fatal("password hash is not independently salted")
	}
	if !CheckPassword(one, password) || CheckPassword(one, strings.TrimSpace(password)) || CheckPassword(one, "wrong-password") {
		t.Fatal("password comparison or whitespace preservation failed")
	}
	for _, bad := range []string{"short", strings.Repeat("x", 129), "null\x00password", string([]byte{0xff, 0xfe})} {
		if ValidatePassword(bad) == nil {
			t.Fatal("invalid password accepted")
		}
	}
	for _, bad := range []string{"", "$argon2id$v1$m=999999999,t=3,p=1$bad$bad", strings.Replace(one, "$v1$", "$v2$", 1)} {
		if CheckPassword(bad, password) {
			t.Fatal("malformed/unsupported hash accepted")
		}
	}
}

func TestNormalizeAdministratorEmail(t *testing.T) {
	got, err := NormalizeEmail("  Admin@Example.COM  ")
	if err != nil || got != "admin@example.com" {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"", "not-an-email", "Name <admin@example.com>", "admin@example.com\r\nInjected: x", strings.Repeat("x", 255) + "@example.com"} {
		if _, err = NormalizeEmail(bad); err == nil {
			t.Fatal("invalid email accepted")
		}
	}
}
