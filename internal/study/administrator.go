package study

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"wzstudy/internal/auth"
)

var (
	ErrAdminInitialization = errors.New("SYSTEM_ADMIN_EMAIL and SYSTEM_ADMIN_PASSWORD are required to initialize the first administrator; configure them locally or run start-local.ps1 -InitializeAdmin")
	ErrAdminEmailMismatch  = errors.New("configured system administrator email does not match the stored administrator; existing account and password will not be replaced")
)

// Administrator is the safe public identity. Password hashes never appear here.
type Administrator struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

type AdministratorSettings struct {
	Email       string
	Password    string
	DisplayName string
}

// administratorRecord is used only inside private storage transactions.
type administratorRecord struct {
	Version      int           `json:"version"`
	Profile      Administrator `json:"profile"`
	PasswordHash string        `json:"password_hash"`
}

const administratorKind = "system_administrator"
const administratorID = "singleton"

func readAdministrator(tx Tx) (administratorRecord, error) {
	v, err := recordAs[administratorRecord](tx, administratorKind, administratorID)
	if err != nil {
		return v, err
	}
	if v.Version != 1 || v.Profile.ID == "" || v.Profile.Email == "" || v.PasswordHash == "" {
		return administratorRecord{}, errors.New("invalid stored administrator record")
	}
	return v, nil
}

func (s *Service) Administrator(ctx context.Context) (out Administrator, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		v, e := readAdministrator(tx)
		if e == nil {
			out = v.Profile
		}
		return e
	})
	return
}

// EnsureAdministrator is a startup-only operation, never exposed through HTTP.
// It creates exactly one administrator, preserves an existing password, and
// rejects a different configured email. The transaction rechecks after hashing.
func (s *Service) EnsureAdministrator(ctx context.Context, in AdministratorSettings) (out Administrator, created bool, err error) {
	email := ""
	if in.Email != "" {
		email, err = auth.NormalizeEmail(in.Email)
		if err != nil {
			return out, false, ErrInvalid
		}
	}
	existing, e := s.Administrator(ctx)
	if e == nil {
		if email != "" && email != existing.Email {
			return out, false, ErrAdminEmailMismatch
		}
		return existing, false, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return out, false, e
	}
	if email == "" || auth.ValidatePassword(in.Password) != nil {
		return out, false, ErrAdminInitialization
	}
	name := strings.TrimSpace(in.DisplayName)
	if name == "" {
		name = "温知管理员"
	}
	if len(name) > 120 || !utf8.ValidString(name) || strings.ContainsAny(name, "\r\n\x00") {
		return out, false, ErrInvalid
	}
	hash, e := auth.HashPassword(in.Password)
	if e != nil {
		return out, false, e
	}
	next := administratorRecord{Version: 1, Profile: Administrator{ID: uuid.NewString(), Email: email, DisplayName: name, CreatedAt: s.Now().UTC()}, PasswordHash: hash}
	err = s.Store.Update(ctx, func(tx Tx) error {
		old, e := readAdministrator(tx)
		if e == nil {
			if old.Profile.Email != email {
				return ErrAdminEmailMismatch
			}
			out = old.Profile
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		if e = recordSave(tx, administratorKind, administratorID, next); e != nil {
			return e
		}
		out = next.Profile
		created = true
		return nil
	})
	if err != nil {
		out = Administrator{}
		created = false
	}
	return
}

func (s *Service) AuthenticateAdministrator(ctx context.Context, email, password string) (out Administrator, err error) {
	var stored administratorRecord
	err = s.Store.View(ctx, func(tx Tx) error {
		var e error
		stored, e = readAdministrator(tx)
		return e
	})
	if err != nil && !errors.Is(err, ErrNotFound) {
		return out, err
	}
	normalized, e := auth.NormalizeEmail(email)
	if err != nil || e != nil || normalized != stored.Profile.Email {
		auth.CheckDummyPassword(password)
		return out, ErrUnauthorized
	}
	if !auth.CheckPassword(stored.PasswordHash, password) {
		return out, ErrUnauthorized
	}
	return stored.Profile, nil
}
