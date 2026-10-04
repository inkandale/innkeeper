package blobstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s := &Store{Name: "library", Root: t.TempDir()}
	if err := os.MkdirAll(s.TmpDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	return s
}

func sum(b string) string {
	h := sha256.Sum256([]byte(b))
	return hex.EncodeToString(h[:])
}

func TestPut(t *testing.T) {
	s := newStore(t)
	sha, size, err := s.Put(strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if sha != sum("hello") || size != 5 {
		t.Fatalf("got %s %d", sha, size)
	}
	path, _ := s.Path(sha)
	if path != filepath.Join(s.Root, sha[:2], sha[2:4], sha) {
		t.Fatalf("unexpected path %s", path)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "hello" {
		t.Fatalf("read back %q, %v", b, err)
	}
	// Same bytes again dedupe to the same file.
	if again, _, err := s.Put(strings.NewReader("hello")); err != nil || again != sha {
		t.Fatalf("second put: %s, %v", again, err)
	}
	if left, _ := os.ReadDir(s.stagingDir()); len(left) != 0 {
		t.Fatalf("staging not empty: %v", left)
	}
}

func TestIngest(t *testing.T) {
	s := newStore(t)
	src := filepath.Join(s.TmpDir(), "card.png")
	os.WriteFile(src, []byte("png bytes"), 0o644)
	sha, size, err := s.Ingest(src)
	if err != nil {
		t.Fatal(err)
	}
	if sha != sum("png bytes") || size != 9 {
		t.Fatalf("got %s %d", sha, size)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source file still in tmp")
	}
}

func TestIngestRejectsUntrustedPaths(t *testing.T) {
	s := newStore(t)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0o600)

	nested := filepath.Join(s.TmpDir(), "sub")
	os.Mkdir(nested, 0o755)
	os.WriteFile(filepath.Join(nested, "f"), []byte("x"), 0o644)

	symlink := filepath.Join(s.TmpDir(), "link")
	os.Symlink(outside, symlink)

	hardlink := filepath.Join(s.TmpDir(), "hard")
	os.Link(outside, hardlink)

	for name, path := range map[string]string{
		"outside tmp": outside,
		"dot-dot":     filepath.Join(s.TmpDir(), "..", "..", filepath.Base(filepath.Dir(outside)), "secret"),
		"nested":      filepath.Join(nested, "f"),
		"symlink":     symlink,
		"hard link":   hardlink,
		"directory":   nested,
		"missing":     filepath.Join(s.TmpDir(), "missing"),
	} {
		if _, _, err := s.Ingest(path); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: Ingest(%s) = %v, want ErrInvalid", name, path, err)
		}
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "secret" {
		t.Fatalf("outside file disturbed: %q, %v", b, err)
	}
	if fi, _ := os.Stat(outside); fi.Mode().Perm() != 0o600 {
		t.Fatalf("outside file mode changed to %v", fi.Mode().Perm())
	}
}

func TestIngestStoreFailureIsNotInvalid(t *testing.T) {
	s := newStore(t)
	src := filepath.Join(s.TmpDir(), "card.png")
	os.WriteFile(src, []byte("png bytes"), 0o644)
	if err := os.Mkdir(s.stagingDir(), 0o555); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Ingest(src); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("Ingest with unwritable staging = %v, want a non-ErrInvalid error", err)
	}
}

func TestPathRejectsBadHash(t *testing.T) {
	s := newStore(t)
	for _, sha := range []string{"", "../../etc/passwd", strings.Repeat("A", 64)} {
		if _, err := s.Path(sha); err == nil {
			t.Errorf("Path(%q) accepted", sha)
		}
	}
}
