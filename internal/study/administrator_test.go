package study

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const administratorTestPassword = "  administrator-test-password  "

func TestAdministratorBootstrapIdempotencyAndPrivateRecord(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	if _, _, err := s.EnsureAdministrator(ctx, AdministratorSettings{}); !errors.Is(err, ErrAdminInitialization) {
		t.Fatal(err)
	}
	if _, err := s.Administrator(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed bootstrap saved an account", err)
	}
	item, err := s.SaveManaged(ctx, "", ManagedInput{Status: "draft", Content: Content{Title: "existing draft", Content: "must remain untouched", Format: "text"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	profile, created, err := s.EnsureAdministrator(ctx, AdministratorSettings{Email: " Admin@Example.COM ", Password: administratorTestPassword, DisplayName: "系统管理员"})
	if err != nil || !created || profile.Email != "admin@example.com" || profile.DisplayName != "系统管理员" {
		t.Fatal(profile, created, err)
	}
	public, _ := json.Marshal(profile)
	if strings.Contains(string(public), "password") || strings.Contains(string(public), "argon2") {
		t.Fatal("public profile exposes password material")
	}
	if err = s.Store.View(ctx, func(tx Tx) error {
		raw, e := tx.Record(administratorKind, administratorID)
		if e != nil {
			return e
		}
		if strings.Contains(string(raw), administratorTestPassword) || !strings.Contains(string(raw), "$argon2id$") {
			t.Fatal("plain password persisted or hash missing")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, settings := range []AdministratorSettings{{}, {Email: "ADMIN@example.com", Password: "different-new-password"}} {
		got, made, err := s.EnsureAdministrator(ctx, settings)
		if err != nil || made || got.ID != profile.ID {
			t.Fatal("bootstrap is not idempotent", made, err)
		}
	}
	if _, _, err = s.EnsureAdministrator(ctx, AdministratorSettings{Email: "other@example.com", Password: "different-new-password"}); !errors.Is(err, ErrAdminEmailMismatch) {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateAdministrator(ctx, "ADMIN@example.com", administratorTestPassword); err != nil {
		t.Fatal(err)
	}
	for _, in := range [][2]string{{"admin@example.com", "different-new-password"}, {"other@example.com", administratorTestPassword}, {"not-mail", administratorTestPassword}} {
		if _, err = s.AuthenticateAdministrator(ctx, in[0], in[1]); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("credentials unexpectedly accepted", err)
		}
	}
	stored, err := s.Item(ctx, item.ID)
	if err != nil || stored.Draft.Content != "must remain untouched" {
		t.Fatal("bootstrap changed existing workspace data", err)
	}
}

func TestAdministratorFileRestartAndNoPasswordInSnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.json")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(store)
	first, _, err := s.EnsureAdministrator(ctx, AdministratorSettings{Email: "admin@example.com", Password: administratorTestPassword})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(raw), administratorTestPassword) {
		t.Fatal("snapshot contains plain password", err)
	}
	store.Close()
	reopened, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s = NewService(reopened)
	profile, created, err := s.EnsureAdministrator(ctx, AdministratorSettings{})
	if err != nil || created || profile.ID != first.ID {
		t.Fatal("restart needed initialization credentials", err)
	}
	if _, err = s.AuthenticateAdministrator(ctx, first.Email, administratorTestPassword); err != nil {
		t.Fatal(err)
	}
}

func TestAdministratorConcurrentFirstInitialization(t *testing.T) {
	s := NewService(NewMemoryStore())
	ctx := context.Background()
	var wg sync.WaitGroup
	created := make(chan bool, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, made, err := s.EnsureAdministrator(ctx, AdministratorSettings{Email: "admin@example.com", Password: administratorTestPassword})
			created <- made
			errs <- err
		}()
	}
	wg.Wait()
	close(created)
	close(errs)
	count := 0
	for made := range created {
		if made {
			count++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatal("more than one first administrator", count)
	}
}
