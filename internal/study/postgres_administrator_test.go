package study

import (
	"context"
	"errors"
	"testing"
)

// Dedicated TEST_DATABASE_URL only; the fixture isolates its schema.
func TestPostgresAdministratorInitialization(t *testing.T) {
	s := fixture(t, "postgres")
	ctx := context.Background()
	profile, created, err := s.EnsureAdministrator(ctx, AdministratorSettings{Email: "Admin@Example.COM", Password: administratorTestPassword})
	if err != nil || !created {
		t.Fatal(err)
	}
	reopened := NewService(s.Store)
	got, made, err := reopened.EnsureAdministrator(ctx, AdministratorSettings{Email: "admin@example.com", Password: "this-must-not-replace-password"})
	if err != nil || made || got.ID != profile.ID {
		t.Fatal(err)
	}
	if _, err = reopened.AuthenticateAdministrator(ctx, "admin@example.com", administratorTestPassword); err != nil {
		t.Fatal(err)
	}
	if _, _, err = reopened.EnsureAdministrator(ctx, AdministratorSettings{Email: "other@example.com", Password: administratorTestPassword}); !errors.Is(err, ErrAdminEmailMismatch) {
		t.Fatal(err)
	}
}
