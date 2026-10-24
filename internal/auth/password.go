// Package auth contains password primitives; it has no storage or HTTP dependencies.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const passwordParameters = "m=65536,t=3,p=1"

// NormalizeEmail follows the system-administrator configuration in wenzflow.
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if len(email) == 0 || len(email) > 254 || !utf8.ValidString(email) || strings.ContainsAny(email, "\r\n\x00") {
		return "", errors.New("invalid email")
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return "", errors.New("invalid email")
	}
	return email, nil
}

// Passwords are not trimmed. Limits are UTF-8 bytes, matching the reference server.
func ValidatePassword(password string) error {
	if len(password) < 8 || len(password) > 128 || !utf8.ValidString(password) || strings.ContainsRune(password, '\x00') {
		return errors.New("password must contain 8-128 UTF-8 bytes without NUL")
	}
	return nil
}

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, 32)
	return fmt.Sprintf("$argon2id$v1$%s$%s$%s", passwordParameters, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func CheckPassword(encoded, password string) bool {
	if ValidatePassword(password) != nil {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v1" || parts[3] != passwordParameters {
		return false // Never accept arbitrary memory/work parameters from stored data.
	}
	salt, saltErr := base64.RawStdEncoding.DecodeString(parts[4])
	want, keyErr := base64.RawStdEncoding.DecodeString(parts[5])
	if saltErr != nil || keyErr != nil || len(salt) != 16 || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}

var dummyOnce sync.Once
var dummyHash string

// Unknown email addresses still perform the same expensive password verification.
func CheckDummyPassword(password string) {
	dummyOnce.Do(func() { dummyHash, _ = HashPassword("wzstudy-invalid-account-only") })
	_ = CheckPassword(dummyHash, password)
}
