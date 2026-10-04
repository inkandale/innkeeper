package secrets

import (
	"bytes"
	"encoding/base64"
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
