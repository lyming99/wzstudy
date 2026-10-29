package study

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUserLearningIsolationAndPersistence(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			var service *Service
			var path string
			if backend == "file" {
				path = filepath.Join(t.TempDir(), "workspace.json")
				store, err := OpenFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				service = NewService(store)
			} else {
				service = fixture(t, backend)
			}
			alice, err := service.CreateUser(ctx, UserInput{Email: "ALICE@example.test", DisplayName: "Alice", Password: "user-password-alice"})
			if err != nil || alice.Email != "alice@example.test" {
				t.Fatal(err)
			}
			bob, err := service.CreateUser(ctx, UserInput{Email: "bob@example.test", DisplayName: "Bob", Password: "user-password-bob"})
			if err != nil || bob.ID == alice.ID {
				t.Fatal(err)
			}
			if _, err = service.CreateUser(ctx, UserInput{Email: alice.Email, DisplayName: "duplicate", Password: "user-password-other"}); !errors.Is(err, ErrConflict) {
				t.Fatal("duplicate email", err)
			}
			_, a1, _, err := service.LoginUser(ctx, alice.Email, "user-password-alice")
			if err != nil {
				t.Fatal(err)
			}
			_, a2, _, err := service.LoginUser(ctx, alice.Email, "user-password-alice")
			if err != nil || a1 == a2 {
				t.Fatal(err)
			}
			_, b, _, err := service.LoginUser(ctx, bob.Email, "user-password-bob")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err = service.LoginUser(ctx, "missing@example.test", "user-password-bob"); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("unknown user", err)
			}
			if _, _, _, err = service.LoginUser(ctx, alice.Email, "user-password-bob"); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("wrong password", err)
			}
			source, key := newSource(t, service, "user-content", true)
			item := ingestOne(t, service, source, key, "lesson", "shared public material")
			event := LearningEventInput{EventID: uuid.NewString(), ItemID: item.ID, Action: "view"}
			if _, err = service.ReportLearning(ctx, a1, []LearningEventInput{event}); err != nil {
				t.Fatal(err)
			}
			ack, err := service.ReportLearning(ctx, a2, []LearningEventInput{event})
			if err != nil || ack.Duplicates != 1 {
				t.Fatal("same user across devices is not idempotent", ack, err)
			}
			other, err := service.ClientLearning(ctx, b, "", nil, 20, 0)
			if err != nil || other.Total != 0 {
				t.Fatal("cross-user leak", other, err)
			}
			ack, err = service.ReportLearning(ctx, b, []LearningEventInput{event})
			if err != nil || ack.Accepted != 1 {
				t.Fatal("event ID collided across users", ack, err)
			}
			if err = service.LogoutUser(ctx, a1); err != nil {
				t.Fatal(err)
			}
			if _, err = service.UserForSession(ctx, a1); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("logged-out session accepted", err)
			}
			if backend == "file" {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{"user-password-alice", "user-password-bob", a1, a2, b} {
					if strings.Contains(string(raw), secret) {
						t.Fatal("plaintext secret persisted")
					}
				}
				service.Store.Close()
				store, err := OpenFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				service = NewService(store)
			}
			own, err := service.ClientLearning(ctx, a2, "", nil, 20, 0)
			if err != nil || own.ClientID != alice.ID || own.Total != 1 || own.Items[0].ViewCount != 1 {
				t.Fatal("owner or persistence changed", own, err)
			}
			updated, err := service.UpdateUser(ctx, alice.ID, alice.Revision, UserInput{Email: alice.Email, DisplayName: "Alice renamed", Password: "reset-password-alice"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.UserForSession(ctx, a2); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("reset did not revoke all sessions", err)
			}
			_, fresh, _, err := service.LoginUser(ctx, alice.Email, "reset-password-alice")
			if err != nil {
				t.Fatal(err)
			}
			own, err = service.ClientLearning(ctx, fresh, "", nil, 20, 0)
			if err != nil || own.Total != 1 || own.ClientID != alice.ID {
				t.Fatal("password reset lost data", err)
			}
			no := false
			updated, err = service.UpdateUser(ctx, alice.ID, updated.Revision, UserInput{Email: updated.Email, DisplayName: updated.DisplayName, Enabled: &no})
			if err != nil {
				t.Fatal(err)
			}
			yes := true
			_, err = service.UpdateUser(ctx, alice.ID, updated.Revision, UserInput{Email: updated.Email, DisplayName: updated.DisplayName, Enabled: &yes})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.UserForSession(ctx, fresh); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("re-enabling resurrected old session", err)
			}
			if _, err = service.UserForSession(ctx, b); err != nil {
				t.Fatal("other user session affected", err)
			}
		})
	}
}

func TestUserSessionExpiryAndBound(t *testing.T) {
	s := NewService(NewMemoryStore())
	ctx := context.Background()
	user, err := s.CreateUser(ctx, UserInput{Email: "expiry@example.test", DisplayName: "expiry", Password: "expiry-password"})
	if err != nil {
		t.Fatal(err)
	}
	var first, last string
	for i := 0; i < 17; i++ {
		_, token, _, e := s.LoginUser(ctx, user.Email, "expiry-password")
		if e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			first = token
		}
		last = token
	}
	if _, err = s.UserForSession(ctx, first); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old sessions are unbounded", err)
	}
	if _, err = s.UserForSession(ctx, last); err != nil {
		t.Fatal(err)
	}
	now := s.Now()
	s.Now = func() time.Time { return now.Add(UserSessionLifetime + time.Second) }
	if _, err = s.UserForSession(ctx, last); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired session accepted", err)
	}
}
