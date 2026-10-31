package study

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

var (
	ErrClientKeyMismatch      = errors.New("client key does not match the current identity")
	ErrClientVaultUnavailable = errors.New("client key vault unavailable")
)

// ConfigureClientKeyVault derives a domain-separated encryption key from the
// stable deployment secret. It must be configured before serving requests.
// A missing/short secret leaves legacy hash-only behavior available.
func (s *Service) ConfigureClientKeyVault(secret string) {
	s.clientKeyMu.Lock()
	defer s.clientKeyMu.Unlock()
	s.clientKeyVault = nil
	if len(secret) < 32 {
		return
	}
	key := sha256.Sum256([]byte("wzstudy-query-client-key-v1\x00" + secret))
	s.clientKeyVault = append([]byte(nil), key[:]...)
}

func (s *Service) clientKeyAEAD() (cipher.AEAD, error) {
	s.clientKeyMu.RLock()
	key := append([]byte(nil), s.clientKeyVault...)
	s.clientKeyMu.RUnlock()
	if len(key) == 0 {
		return nil, ErrClientVaultUnavailable
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func clientKeyAAD(client Client) []byte {
	return []byte("wzstudy-query-client-key-v1\x00" + client.OwnerID + "\x00" + client.ID)
}

// sealClientKey keeps authentication hash-only while retaining an authenticated
// encrypted copy for the owner's QR dialog. A service without a configured
// vault remains backward-compatible and does not retain a recoverable copy.
func (s *Service) sealClientKey(client Client, key string) (string, error) {
	aead, err := s.clientKeyAEAD()
	if errors.Is(err, ErrClientVaultUnavailable) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(key), clientKeyAAD(client))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// ClientSecret returns a key only for a record in the current workspace. Old
// hash-only records or ciphertext encrypted under a lost/changed vault secret
// return an empty key. They continue to authenticate via the original hash.
func (s *Service) ClientSecret(ctx context.Context, id string) (out string, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		record, e := recordAs[clientRecord](tx, "client", id)
		if e != nil {
			return e
		}
		if record.Secret.KeyCiphertext == "" {
			return nil
		}
		aead, e := s.clientKeyAEAD()
		if e != nil {
			return nil
		}
		sealed, e := base64.RawURLEncoding.DecodeString(record.Secret.KeyCiphertext)
		if e != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
			return nil
		}
		key, e := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], clientKeyAAD(record.Client))
		if e == nil && EqualKey(record.Secret.KeyHash, string(key)) {
			out = string(key)
		}
		return nil
	})
	return
}

// RememberClientKey explicitly restores a retained copy without changing the
// current key or its revision. The hash comparison and save share one write
// transaction, so a concurrent key rotation cannot restore the old key.
func (s *Service) RememberClientKey(ctx context.Context, id, key string) error {
	if len(key) < 32 || len(key) > 256 {
		return ErrClientKeyMismatch
	}
	return s.Store.Update(ctx, func(tx Tx) error {
		record, err := recordAs[clientRecord](tx, "client", id)
		if err != nil {
			return err
		}
		if !EqualKey(record.Secret.KeyHash, key) {
			return ErrClientKeyMismatch
		}
		sealed, err := s.sealClientKey(record.Client, key)
		if err != nil {
			return err
		}
		if sealed == "" {
			return ErrClientVaultUnavailable
		}
		record.Secret.KeyCiphertext = sealed
		return recordSave(tx, "client", id, record)
	})
}
