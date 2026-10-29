package study

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestAdministratorWorkspaceAndSessionSurviveRestartWithoutChangingOwner(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.json")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(store)
	admin, _, err := s.EnsureAdministrator(ctx, AdministratorSettings{Email: "admin@example.test", Password: administratorTestPassword})
	if err != nil {
		t.Fatal(err)
	}
	user, token, _, err := s.LoginAdministratorUser(ctx, admin.Email, administratorTestPassword)
	if err != nil || user.ID != admin.ID || token == "" {
		t.Fatal("admin user login failed", err)
	}
	ownerContext := WithWorkspace(ctx, user.ID)
	item, err := s.SaveManaged(ownerContext, "", ManagedInput{Status: "draft", Content: Content{Title: "admin own item", Content: "private", Format: "text"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	reopened, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s = NewService(reopened)
	if _, made, e := s.EnsureAdministrator(ctx, AdministratorSettings{Email: admin.Email, Password: "different-initial-password"}); e != nil || made {
		t.Fatal("restart replaced admin", made, e)
	}
	restored, err := s.UserForSession(ctx, token)
	if err != nil || restored.ID != admin.ID {
		t.Fatal("admin user session did not survive restart", err)
	}
	if _, err = s.AdministratorForUserSession(ctx, token); err != nil {
		t.Fatal("admin capability lost on restart", err)
	}
	actual, err := s.Item(WithWorkspace(ctx, restored.ID), item.ID)
	if err != nil || actual.OwnerID != admin.ID || actual.Draft.Title != "admin own item" {
		t.Fatal("admin workspace did not survive restart", err)
	}
	if err = s.ChangeUserPassword(ctx, token, administratorTestPassword, "new-administrator-password"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UserForSession(ctx, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old session survived password change", err)
	}
	if _, err = s.AuthenticateAdministrator(ctx, admin.Email, administratorTestPassword); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old administrator password survived change", err)
	}
	if _, _, _, err = s.LoginAdministratorUser(ctx, admin.Email, "new-administrator-password"); err != nil {
		t.Fatal("new administrator password failed", err)
	}
}
