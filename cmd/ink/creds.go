package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
)

// creds are a profile's OAuth tokens.
type creds struct {
	Access  string    `json:"access"`
	Expires time.Time `json:"expires"`
	Refresh string    `json:"refresh"`
}

const keyringService = "ink"

var errNoCreds = errors.New("no saved login")

// credsFile is where a profile's tokens go when there is no keychain.
func credsFile(name string) (string, error) {
	if err := checkProfileName(name); err != nil {
		return "", err
	}
	dir, err := configDir()
	return filepath.Join(dir, name+".creds"), err
}

// saveCreds stores a profile's tokens in the OS keychain or, where there is
// none, in a file only the user can read. It returns which.
func saveCreds(name string, c creds) (string, error) {
	path, err := credsFile(name)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	if keyring.Set(keyringService, name, string(b)) == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		return "the keychain", nil
	}
	return path, writePrivate(path, b)
}

// writePrivate replaces path with b, readable only by the user. The temp file
// and rename keep readers from seeing a partial file and reset looser modes.
func writePrivate(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// loadCreds returns a profile's tokens. A file means the keychain failed
// when they were saved, so it comes first.
func loadCreds(name string) (creds, error) {
	var c creds
	path, err := credsFile(name)
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		s, kerr := keyring.Get(keyringService, name)
		if errors.Is(kerr, keyring.ErrNotFound) {
			return c, errNoCreds
		}
		if kerr != nil {
			// Keep errNoCreds so callers still print the login hint.
			return c, fmt.Errorf("%w (keychain: %v)", errNoCreds, kerr)
		}
		b, err = []byte(s), nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func deleteCreds(name string) error {
	path, err := credsFile(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// The keychain may hold an older copy from before a fallback save. Its
	// errors don't matter: a keychain that can't be reached can't hold the login.
	_ = keyring.Delete(keyringService, name)
	return nil
}
