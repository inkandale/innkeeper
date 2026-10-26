// Package secrets seals plugin instance secrets for storage.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// EnvVar holds the key: 32 random bytes, base64-encoded.
const EnvVar = "INNKEEPER_SECRETS_KEY"

// KeyFileEnvVar names a file that holds the key when EnvVar is unset. The
// first process to start creates it.
const KeyFileEnvVar = "INNKEEPER_SECRETS_KEY_FILE"

// Box seals and opens secrets with AES-256-GCM.
type Box struct {
	aead cipher.AEAD
	key  []byte
}

// FromEnv returns a Box with the key in EnvVar, or else in the file
// KeyFileEnvVar names.
func FromEnv() (*Box, error) {
	from, v := EnvVar, os.Getenv(EnvVar)
	if v == "" {
		from = os.Getenv(KeyFileEnvVar)
		if from == "" {
			return nil, fmt.Errorf("%s is not set; generate one with: openssl rand -base64 32", EnvVar)
		}
		var err error
		if v, err = readOrCreateKey(from); err != nil {
			return nil, err
		}
	}
	key, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid base64: %w", from, err)
	}
	return New(key)
}

// readOrCreateKey reads the key in path, creating it if it is missing.
func readOrCreateKey(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		err = createKey(path)
		if err == nil || errors.Is(err, fs.ErrExist) {
			raw, err = os.ReadFile(path)
		}
	}
	if err != nil {
		return "", fmt.Errorf("secrets key file: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// createKey writes a new key to path, or fails with fs.ErrExist if another
// process got there first. Linking a finished temp file means no process can
// read a half-written key.
func createKey(path string) error {
	key := make([]byte, 32)
	rand.Read(key)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".secrets-key-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(base64.StdEncoding.EncodeToString(key) + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmp.Name(), path)
}

// New returns a Box with a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets key is %d bytes, want 32", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, key: key}, nil
}

// Seal encrypts secrets behind a random nonce. No secrets seal to nil.
func (b *Box) Seal(secrets map[string]string) ([]byte, error) {
	if len(secrets) == 0 {
		return nil, nil
	}
	plain, err := json.Marshal(secrets)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, b.aead.NonceSize())
	rand.Read(nonce)
	return b.aead.Seal(nonce, nonce, plain, nil), nil
}

// Open decrypts what Seal returned. Nil opens to an empty map.
func (b *Box) Open(sealed []byte) (map[string]string, error) {
	out := map[string]string{}
	if len(sealed) == 0 {
		return out, nil
	}
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("sealed secrets are too short")
	}
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, fmt.Errorf("open secrets: wrong key or damaged data: %w", err)
	}
	return out, json.Unmarshal(plain, &out)
}

// Derive returns a stable secret for label, so the API and worker agree on
// it without storing it. Different labels or keys give different values.
func (b *Box) Derive(label string) string {
	h := hmac.New(sha256.New, b.key)
	h.Write([]byte(label))
	return hex.EncodeToString(h.Sum(nil))
}
