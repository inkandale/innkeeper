package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain keeps tests away from the user's config, keychain, and token.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ink-test")
	if err != nil {
		panic(err)
	}
	configDir = func() (string, error) { return dir, nil }
	keyring.MockInit()
	os.Unsetenv("INNKEEPER_URL")
	os.Unsetenv("INNKEEPER_TOKEN")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// freshConfig gives the test an empty config directory and keychain.
func freshConfig(t *testing.T) {
	old := configDir
	dir := t.TempDir()
	configDir = func() (string, error) { return dir, nil }
	keyring.MockInit()
	t.Cleanup(func() {
		configDir = old
		keyring.MockInit()
	})
}

func TestConfigRoundTrip(t *testing.T) {
	freshConfig(t)
	c, err := loadConfig()
	if err != nil || len(c.Profiles) != 0 {
		t.Fatalf("empty config = %+v, %v", c, err)
	}
	c.Default = "home"
	c.Profiles["home"] = profile{URL: "https://inn.example", ClientID: "inkc_1"}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig()
	if err != nil || got.Default != "home" || got.Profiles["home"] != c.Profiles["home"] {
		t.Fatalf("config = %+v, %v", got, err)
	}
}

func TestCredsFallBackToAFile(t *testing.T) {
	freshConfig(t)
	if where, err := saveCreds("home", creds{Access: "a1", Refresh: "r1"}); err != nil || where != "the keychain" {
		t.Fatalf("saved to %q, %v", where, err)
	}
	keyring.MockInitWithError(errors.New("no keychain"))
	where, err := saveCreds("home", creds{Access: "a2", Refresh: "r2"})
	dir, _ := configDir()
	if err != nil || where != filepath.Join(dir, "home.creds") {
		t.Fatalf("saved to %q, %v", where, err)
	}
	if fi, err := os.Stat(where); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("creds file: %v, %v", fi.Mode(), err)
	}
	if c, err := loadCreds("home"); err != nil || c.Refresh != "r2" {
		t.Fatalf("loaded %+v, %v", c, err)
	}
	if err := deleteCreds("home"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCreds("home"); err == nil {
		t.Fatal("creds outlived deleteCreds")
	}
}

func TestCredsRefuseABadProfileName(t *testing.T) {
	freshConfig(t)
	if _, err := saveCreds("../x", creds{}); err == nil || !strings.Contains(err.Error(), "profile name") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteCredsRemovesTheKeychainCopyToo(t *testing.T) {
	freshConfig(t)
	if _, err := saveCreds("home", creds{Access: "a1", Refresh: "r1"}); err != nil {
		t.Fatal(err)
	}
	// A later save that fell back to the file leaves the keychain copy behind.
	path, _ := credsFile("home")
	if err := os.WriteFile(path, []byte(`{"access":"a2","refresh":"r2"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := deleteCreds("home"); err != nil {
		t.Fatal(err)
	}
	if c, err := loadCreds("home"); !errors.Is(err, errNoCreds) {
		t.Fatalf("loaded %+v, %v", c, err)
	}
}

func TestLoadCredsKeepsTheLoginHintWhenTheKeychainIsDown(t *testing.T) {
	freshConfig(t)
	keyring.MockInitWithError(errors.New("no dbus"))
	_, err := loadCreds("home")
	if !errors.Is(err, errNoCreds) || !strings.Contains(err.Error(), "no dbus") {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveTightensAnExistingConfigFile(t *testing.T) {
	freshConfig(t)
	path, _ := configFile()
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (config{Profiles: map[string]profile{}}).save(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("config file: %v, %v", fi.Mode(), err)
	}
}
