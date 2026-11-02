package study

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestLearningChannelStoreContract(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			s := fixture(t, kind)
			base := context.Background()
			owner, e := s.CreateUser(base, UserInput{Email: "channel-owner@example.test", DisplayName: "渠道提供者", Password: "test-channel-password"})
			if e != nil {
				t.Fatal(e)
			}
			reader, e := s.CreateUser(base, UserInput{Email: "channel-reader@example.test", DisplayName: "学习者", Password: "test-channel-password"})
			if e != nil {
				t.Fatal(e)
			}
			_, token, _, e := s.LoginUser(base, reader.Email, "test-channel-password")
			if e != nil {
				t.Fatal(e)
			}
			a := WithWorkspace(base, owner.ID)
			b := WithWorkspace(base, reader.ID)
			item, e := s.SaveManaged(a, "", ManagedInput{Status: "pending", Content: Content{Title: "published lesson", Content: "published body"}}, nil)
			if e != nil {
				t.Fatal(e)
			}
			item, e = s.Publication(a, item.ID, item.Revision, true)
			if e != nil {
				t.Fatal(e)
			}
			channel, e := s.OwnLearningChannel(a)
			if e != nil || channel.ID != owner.ID || channel.Visibility != "private" {
				t.Fatal(channel, e)
			}
			channel, e = s.SaveLearningChannel(a, LearningChannelInput{Name: "shared", Visibility: "invited", SourceIDs: []string{item.SourceID}, InvitedEmails: []string{reader.Email}})
			if e != nil {
				t.Fatal(e)
			}
			page, e := s.LearningChannelItems(b, channel.ID, Filter{Limit: 12})
			if e != nil || len(page.Items) != 1 || page.Items[0].ID != item.ID {
				t.Fatal(page, e)
			}
			// The normal content namespace is not widened by reading a channel.
			if _, e = s.Item(b, item.ID); !errors.Is(e, ErrNotFound) {
				t.Fatal("shared content escaped to ordinary workspace", e)
			}
			learningCtx := WithSharedLearning(b)
			result, e := s.ReportLearning(learningCtx, token, []LearningEventInput{{EventID: uuid.NewString(), ItemID: item.ID, Action: "view"}})
			if e != nil || result.Accepted != 1 || result.States[0].ClientID != reader.ID {
				t.Fatal(result, e)
			}
			// Inspect the publisher namespace without fabricating a query Client or
			// accepting a caller-supplied learner identity.
			if e = s.Store.View(a, func(tx Tx) error {
				records, e := recordsAs[LearningState](tx, "learning_state")
				if e != nil {
					return e
				}
				if len(records) != 0 {
					t.Fatal("visitor learning persisted in publisher namespace")
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			channel, e = s.SaveLearningChannel(a, LearningChannelInput{Revision: channel.Revision, Name: channel.Name, Visibility: "private", SourceIDs: channel.SourceIDs})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.LearningChannelItems(b, channel.ID, Filter{Limit: 12}); !errors.Is(e, ErrNotFound) {
				t.Fatal("revocation failed", e)
			}
			if _, e = s.ReportLearning(learningCtx, token, []LearningEventInput{{EventID: uuid.NewString(), ItemID: item.ID, Action: "view"}}); !errors.Is(e, ErrNotFound) {
				t.Fatal("revoked learning accepted", e)
			}
			if e = s.Store.Ping(base); e != nil {
				t.Fatal("store not ready after migrations", e)
			}
		})
	}
}

func TestPostgresLearningChannelUniqueAndReadiness(t *testing.T) {
	s := fixture(t, "postgres")
	ctx := context.Background()
	user, e := s.CreateUser(ctx, UserInput{Email: "channel-unique@example.test", DisplayName: "unique", Password: "test-channel-password"})
	if e != nil {
		t.Fatal(e)
	}
	owned := WithWorkspace(ctx, user.ID)
	channel, e := s.SaveLearningChannel(owned, LearningChannelInput{Name: "unique", Visibility: "private"})
	if e != nil {
		t.Fatal(e)
	}
	e = s.Store.Update(owned, func(tx Tx) error { return recordSave(tx, "learning_channel", uuid.NewString(), channel) })
	if !errors.Is(e, ErrConflict) {
		t.Fatal("database did not enforce one channel per workspace", e)
	}
	scoped := s.Store.(*workspaceStore)
	postgres := scoped.Store.(*PostgresStore)
	if _, e = postgres.Pool.Exec(ctx, "DROP INDEX workspace_learning_channel_owner"); e != nil {
		t.Fatal(e)
	}
	if e = s.Store.Ping(ctx); e == nil || !strings.Contains(e.Error(), "learning channel schema migration required") {
		t.Fatal("missing migration still reported ready", e)
	}
}
