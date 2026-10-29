package study

import (
	"context"
	"errors"
	"testing"
)

func TestNewAccountsCannotDuplicateAdministratorEmail(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	admin, _, err := s.EnsureAdministrator(ctx, AdministratorSettings{Email: "admin@example.test", Password: administratorTestPassword})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateUser(ctx, UserInput{Email: " ADMIN@example.test ", DisplayName: "duplicate", Password: "learner-password"})
	if !errors.Is(err, ErrAccountEmailConflict) || !errors.Is(err, ErrConflict) {
		t.Fatal("administrator email duplicated", err)
	}
	user, err := s.CreateUser(ctx, UserInput{Email: "other@example.test", DisplayName: "learner", Password: "learner-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateUser(ctx, user.ID, user.Revision, UserInput{Email: admin.Email, DisplayName: user.DisplayName})
	if !errors.Is(err, ErrAccountEmailConflict) {
		t.Fatal("ordinary user renamed to administrator email", err)
	}
	after, err := s.User(ctx, user.ID)
	if err != nil || after.Email != user.Email || after.Revision != user.Revision {
		t.Fatal("conflict changed existing user", after, err)
	}
	if _, err := s.AuthenticateAdministrator(ctx, admin.Email, administratorTestPassword); err != nil {
		t.Fatal("conflict changed administrator credentials", err)
	}
}

func TestAdministratorBootstrapRejectsExistingUserEmail(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	user, err := s.CreateUser(ctx, UserInput{Email: "same@example.test", DisplayName: "learner", Password: "learner-password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.EnsureAdministrator(ctx, AdministratorSettings{Email: "SAME@example.test", Password: administratorTestPassword}); !errors.Is(err, ErrAccountEmailConflict) {
		t.Fatal("bootstrap claimed an existing user's email", err)
	}
	if _, err := s.Administrator(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatal("conflicting bootstrap created an administrator", err)
	}
	after, err := s.User(ctx, user.ID)
	if err != nil || after.ID != user.ID || after.Email != user.Email {
		t.Fatal("existing user data changed", after, err)
	}
}
