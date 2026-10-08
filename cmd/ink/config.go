package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// profile is a server ink logged in to. Its tokens are in the token store.
type profile struct {
	URL      string `json:"url"`
	ClientID string `json:"client_id"`
}

type config struct {
	Default  string             `json:"default,omitempty"`
	Profiles map[string]profile `json:"profiles"`
}

// configDir is where ink keeps its config and, without a keychain, its
// tokens. Tests replace it.
var configDir = func() (string, error) {
	d, err := os.UserConfigDir()
	return filepath.Join(d, "ink"), err
}

// A profile name becomes part of a file name.
var profileName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func checkProfileName(name string) error {
	if !profileName.MatchString(name) {
		return fmt.Errorf("profile name %q must be 1 to 32 letters, digits, - or _", name)
	}
	return nil
}

func configFile() (string, error) {
	dir, err := configDir()
	return filepath.Join(dir, "config.json"), err
}

// loadConfig reads the config, or returns an empty one if there is none.
func loadConfig() (config, error) {
	c := config{Profiles: map[string]profile{}}
	path, err := configFile()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if c.Profiles == nil {
		c.Profiles = map[string]profile{}
	}
	return c, nil
}

func (c config) save() error {
	path, err := configFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
