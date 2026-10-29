package study

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestUserWorkspaceIsolation(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var service *Service
			var file string
			if backend == "file" {
				file = filepath.Join(t.TempDir(), "workspace.json")
				store, err := OpenFileStore(file)
				if err != nil {
					t.Fatal(err)
				}
				service = NewService(store)
			} else {
				service = fixture(t, backend)
			}
			root := context.Background()
			a, err := service.CreateUser(root, UserInput{Email: "a@workspace.test", DisplayName: "A", Password: "workspace-password-a"})
			if err != nil {
				t.Fatal(err)
			}
			b, err := service.CreateUser(root, UserInput{Email: "b@workspace.test", DisplayName: "B", Password: "workspace-password-b"})
			if err != nil {
				t.Fatal(err)
			}
			ac, bc := WithWorkspace(root, a.ID), WithWorkspace(root, b.ID)
			as, ak := newSource(t, service, "legacy-root", true)
			legacy := ingestOne(t, service, as, ak, "legacy", "unowned archived data")
			sourceA, sourceKeyA, err := service.CreateSource(ac, SourceInput{Slug: "same-slug", Name: "A source"})
			if err != nil {
				t.Fatal(err)
			}
			sourceB, sourceKeyB, err := service.CreateSource(bc, SourceInput{Slug: "same-slug", Name: "B source"})
			if err != nil {
				t.Fatal("same source slug across users", err)
			}
			ia, err := service.Ingest(ac, sourceA.ID, sourceKeyA, []Content{{ExternalID: "same-external-id", Title: "A private article"}})
			if err != nil {
				t.Fatal(err)
			}
			ib, err := service.Ingest(bc, sourceB.ID, sourceKeyB, []Content{{ExternalID: "same-external-id", Title: "B private article"}})
			if err != nil {
				t.Fatal(err)
			}
			itemA, itemB := ia.Items[0], ib.Items[0]
			for _, tc := range []struct {
				ctx               context.Context
				own, other, owner string
			}{{ac, itemA.ID, itemB.ID, a.ID}, {bc, itemB.ID, itemA.ID, b.ID}} {
				items, err := service.Items(tc.ctx, Filter{Limit: 20})
				if err != nil || len(items) != 1 || items[0].ID != tc.own || items[0].OwnerID != tc.owner {
					t.Fatal("list leak", items, err)
				}
				for _, foreign := range []string{tc.other, legacy.ID} {
					if _, err := service.Item(tc.ctx, foreign); !errors.Is(err, ErrNotFound) {
						t.Fatal("foreign detail", err)
					}
				}
				if err := service.DeleteItem(tc.ctx, tc.other, 1); err == nil {
					t.Fatal("foreign deletion")
				}
			}
			if _, err := service.Ingest(bc, sourceA.ID, sourceKeyA, []Content{{ExternalID: "forged", Title: "bad"}}); err == nil {
				t.Fatal("foreign source accepted")
			}
			clientA, queryKeyA, err := service.CreateClient(ac, ClientInput{Slug: "same-client", Name: "A query"})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = service.CreateClient(bc, ClientInput{Slug: "same-client", Name: "B query"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.Client(bc, clientA.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("client detail leak", err)
			}
			if _, err = service.ClientForKey(bc, queryKeyA, PermissionRead); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("foreign key in scoped transaction", err)
			}
			_, tokenA, _, err := service.LoginUser(root, a.Email, "workspace-password-a")
			if err != nil {
				t.Fatal(err)
			}
			_, tokenB, _, err := service.LoginUser(root, b.Email, "workspace-password-b")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.ReportLearning(ac, tokenA, []LearningEventInput{{EventID: uuid.NewString(), ItemID: itemA.ID, Action: "view"}}); err != nil {
				t.Fatal(err)
			}
			if _, err = service.ReportLearning(bc, tokenB, []LearningEventInput{{EventID: uuid.NewString(), ItemID: itemA.ID, Action: "view"}}); !errors.Is(err, ErrNotFound) {
				t.Fatal("learned foreign material", err)
			}
			if _, err = service.ReportLearning(ac, tokenB, []LearningEventInput{{EventID: uuid.NewString(), ItemID: itemA.ID, Action: "view"}}); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("identity/context mismatch", err)
			}
			changesA, err := service.Changes(ac, 0, 100, nil)
			if err != nil || len(changesA.Changes) != 1 || changesA.HighWatermark != 1 || changesA.Changes[0].ItemID != itemA.ID {
				t.Fatal("A cursor leak", changesA, err)
			}
			changesB, err := service.Changes(bc, 0, 100, nil)
			if err != nil || len(changesB.Changes) != 1 || changesB.HighWatermark != 1 || changesB.Changes[0].ItemID != itemB.ID {
				t.Fatal("B cursor leak", changesB, err)
			}
			_, err = service.SaveSettings(ac, WorkspaceSettings{Name: "A personal workspace"})
			if err != nil {
				t.Fatal(err)
			}
			wb, err := service.Workspace(bc)
			if err != nil || wb.Settings.Name == "A personal workspace" || wb.Summary.Total != 1 || len(wb.Sources) != 1 || len(wb.Clients) != 1 {
				t.Fatal("workspace metadata leak", wb, err)
			}
			if backend == "file" {
				service.Store.Close()
				store, err := OpenFileStore(file)
				if err != nil {
					t.Fatal(err)
				}
				service = NewService(store)
			}
			wa, err := service.Workspace(ac)
			if err != nil || wa.Summary.Total != 1 || wa.Settings.Name != "A personal workspace" {
				t.Fatal("workspace restart", wa, err)
			}
			no := false
			_, err = service.UpdateUser(root, a.ID, a.Revision, UserInput{Email: a.Email, DisplayName: a.DisplayName, Enabled: &no})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.Workspace(ac); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("disabled owner's data still accessible", err)
			}
			if _, err = service.Workspace(bc); err != nil {
				t.Fatal("other user affected", err)
			}
		})
	}
}
