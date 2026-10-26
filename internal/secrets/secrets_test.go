package secrets

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func box(t *testing.T, fill byte) *Box {
	t.Helper()
	b, err := New(bytes.Repeat([]byte{fill}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpen(t *testing.T) {
	b := box(t, 1)
	sealed, err := b.Seal(map[string]string{"api_key": "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("hunter2")) {
		t.Fatal("sealed secrets contain the plain value")
	}
	got, err := b.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if got["api_key"] != "hunter2" || len(got) != 1 {
		t.Fatalf("opened %v", got)
	}

	if _, err := box(t, 2).Open(sealed); err == nil {
		t.Fatal("opened with the wrong key")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := b.Open(sealed); err == nil {
		t.Fatal("opened damaged data")
	}
}

func TestEmpty(t *testing.T) {
	b := box(t, 1)
	sealed, err := b.Seal(nil)
	if err != nil || sealed != nil {
		t.Fatalf("Seal(nil) = %v, %v; want nil, nil", sealed, err)
	}
	got, err := b.Open(nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("Open(nil) = %v, %v; want empty map", got, err)
	}
	if got, err := b.Open([]byte{}); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("Open([]byte{}) = %v, %v; want empty map", got, err)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv(EnvVar, "")
	if _, err := FromEnv(); err == nil {
		t.Fatal("no error for a missing key")
	}
	t.Setenv(EnvVar, base64.StdEncoding.EncodeToString([]byte("short")))
	if _, err := FromEnv(); err == nil {
		t.Fatal("no error for a short key")
	}
	t.Setenv(EnvVar, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if _, err := FromEnv(); err != nil {
		t.Fatal(err)
	}
}

func TestDerive(t *testing.T) {
	b := box(t, 1)
	got := b.Derive("a")
	if got != box(t, 1).Derive("a") {
		t.Fatal("same key and label gave different values")
	}
	if got == b.Derive("b") || got == box(t, 2).Derive("a") {
		t.Fatal("different label or key gave the same value")
	}
}

func TestFromEnvCreatesKeyFileOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.key")
	t.Setenv(EnvVar, "")
	t.Setenv(KeyFileEnvVar, path)
	// The api and worker start together and must end up with one key.
	boxes := make([]*Box, 8)
	errs := make([]error, len(boxes))
	var wg sync.WaitGroup
	for i := range boxes {
		wg.Go(func() { boxes[i], errs[i] = FromEnv() })
	}
	wg.Wait()
	if errs[0] != nil {
		t.Fatal(errs[0])
	}
	sealed, err := boxes[0].Seal(map[string]string{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range boxes {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if _, err := b.Open(sealed); err != nil {
			t.Fatalf("box %d has another key: %v", i, err)
		}
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("key file mode = %v, want 0600", fi.Mode().Perm())
	}
	again, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.Open(sealed); err != nil {
		t.Fatalf("a restart read another key: %v", err)
	}
}

func TestFromEnvPrefersKeyOverKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.key")
	t.Setenv(EnvVar, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	t.Setenv(KeyFileEnvVar, path)
	b, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box(t, 7).Seal(map[string]string{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Open(sealed); err != nil {
		t.Fatalf("did not use %s: %v", EnvVar, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("created the key file anyway: %v", err)
	}
}

func TestFromEnvWithoutKeyOrKeyFileFails(t *testing.T) {
	t.Setenv(EnvVar, "")
	t.Setenv(KeyFileEnvVar, "")
	if _, err := FromEnv(); err == nil {
		t.Fatal("FromEnv succeeded with no key")
	}
}
