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

const testClientVault = "test-vault-secret-never-used-outside-tests-123456"

func TestClientSecretLifecycle(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	service.ConfigureClientKeyVault(testClientVault)
	client, key, err := service.CreateClient(ctx, ClientInput{Slug: "qr-secret", Name: "QR"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := service.ClientSecret(ctx, client.ID); err != nil || got != key {
		t.Fatal("new key not retrievable", err)
	}

	var raw []byte
	if err := service.Store.View(ctx, func(tx Tx) error { var err error; raw, err = tx.Record("client", client.ID); return err }); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) || !strings.Contains(string(raw), "key_ciphertext") || !strings.Contains(string(raw), HashKey(key)) {
		t.Fatal("client record must contain ciphertext and hash, not plaintext")
	}
	client, err = service.UpdateClient(ctx, client.ID, client.Revision, ClientInput{Slug: client.Slug, Name: "Changed", Permissions: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := service.ClientSecret(ctx, client.ID); got != key {
		t.Fatal("edit lost key")
	}

	// Simulate an identity created by a hash-only version.
	if err := service.Store.Update(ctx, func(tx Tx) error {
		record, err := recordAs[clientRecord](tx, "client", client.ID)
		if err != nil {
			return err
		}
		record.Secret.KeyCiphertext = ""
		return recordSave(tx, "client", client.ID, record)
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := service.ClientSecret(ctx, client.ID); err != nil || got != "" {
		t.Fatal("legacy key unexpectedly recoverable", err)
	}
	if err := service.RememberClientKey(ctx, client.ID, strings.Repeat("x", 48)); !errors.Is(err, ErrClientKeyMismatch) {
		t.Fatal("wrong key accepted", err)
	}
	if got, _ := service.ClientSecret(ctx, client.ID); got != "" {
		t.Fatal("wrong key saved")
	}
	if err := service.RememberClientKey(ctx, client.ID, key); err != nil {
		t.Fatal(err)
	}
	if got, _ := service.ClientSecret(ctx, client.ID); got != key {
		t.Fatal("legacy key was not restored")
	}
	reloaded, _ := service.Client(ctx, client.ID)
	if reloaded.Revision != client.Revision {
		t.Fatal("restore changed identity revision")
	}
	if _, err := service.ClientForKey(context.Background(), key, PermissionRead); err != nil {
		t.Fatal("restore invalidated old key", err)
	}

	rotated, newKey, err := service.RotateClientKey(ctx, client.ID, client.Revision)
	if err != nil || newKey == key {
		t.Fatal("rotation failed", err)
	}
	if got, _ := service.ClientSecret(ctx, rotated.ID); got != newKey {
		t.Fatal("rotation did not replace saved copy")
	}
	if err := service.RememberClientKey(ctx, rotated.ID, key); !errors.Is(err, ErrClientKeyMismatch) {
		t.Fatal("old key could be re-saved", err)
	}
	if _, err := service.ClientForKey(context.Background(), key, PermissionRead); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old key still valid", err)
	}
}

func TestClientSecretPersistenceAndVaultChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store)
	service.ConfigureClientKeyVault(testClientVault)
	root := context.Background()
	user, err := service.CreateUser(root, UserInput{Email: "vault@example.test", DisplayName: "Vault", Password: "vault-test-password"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithWorkspace(root, user.ID)
	client, key, err := service.CreateClient(ctx, ClientInput{Slug: "persistent", Name: "Persistent"})
	if err != nil {
		t.Fatal(err)
	}
	service.Store.Close()
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), key) {
		t.Fatal("plaintext query key written to file store")
	}
	store, err = OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	service = NewService(store)
	defer service.Store.Close()
	service.ConfigureClientKeyVault(testClientVault)
	if got, err := service.ClientSecret(ctx, client.ID); err != nil || got != key {
		t.Fatal("key did not survive file store restart", err)
	}
	service.ConfigureClientKeyVault(strings.Repeat("z", 40))
	if got, err := service.ClientSecret(ctx, client.ID); err != nil || got != "" {
		t.Fatal("changed vault unexpectedly decrypted key", err)
	}
	if _, err := service.ClientForKey(root, key, PermissionRead); err != nil {
		t.Fatal("changed vault invalidated authentication", err)
	}
	if err := service.RememberClientKey(ctx, client.ID, key); err != nil {
		t.Fatal(err)
	}
	if got, _ := service.ClientSecret(ctx, client.ID); got != key {
		t.Fatal("could not restore with new vault")
	}
}

func TestClientSecretRaceWithRotation(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	service.ConfigureClientKeyVault(testClientVault)
	client, oldKey, err := service.CreateClient(ctx, ClientInput{Slug: "concurrent", Name: "Concurrent"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var rotatedKey string
	var rotateErr, rememberErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, rotatedKey, rotateErr = service.RotateClientKey(ctx, client.ID, client.Revision)
	}()
	go func() { defer wg.Done(); rememberErr = service.RememberClientKey(ctx, client.ID, oldKey) }()
	wg.Wait()
	if rotateErr != nil || (rememberErr != nil && !errors.Is(rememberErr, ErrClientKeyMismatch)) {
		t.Fatal("concurrent mutation failed", rotateErr, rememberErr)
	}
	if got, err := service.ClientSecret(ctx, client.ID); err != nil || got != rotatedKey {
		t.Fatal("old key won concurrent rotation", err)
	}
}

func TestClientCiphertextCannotBeCopiedToAnotherIdentity(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	service.ConfigureClientKeyVault(testClientVault)
	a, _, err := service.CreateClient(ctx, ClientInput{Slug: "cipher-a", Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := service.CreateClient(ctx, ClientInput{Slug: "cipher-b", Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Store.Update(ctx, func(tx Tx) error {
		ar, err := recordAs[clientRecord](tx, "client", a.ID)
		if err != nil {
			return err
		}
		br, err := recordAs[clientRecord](tx, "client", b.ID)
		if err != nil {
			return err
		}
		br.Secret.KeyCiphertext, br.Secret.KeyHash = ar.Secret.KeyCiphertext, ar.Secret.KeyHash
		return recordSave(tx, "client", b.ID, br)
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := service.ClientSecret(ctx, b.ID); err != nil || got != "" {
		t.Fatal("ciphertext not bound to identity", err)
	}
	// Lists serialize only public client data, never secret fields.
	clients, err := service.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(clients)
	if strings.Contains(string(encoded), "key_ciphertext") || strings.Contains(string(encoded), "key_hash") {
		t.Fatal("list exposed internal secrets")
	}
}
