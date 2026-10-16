package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCollectGarbage(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	put := func(s string) Blob {
		b, err := lib.PutBlob(ctx, strings.NewReader(s))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	orphan, kept, recent := put("orphan"), put("kept"), put("recent")
	if _, _, err := lib.Cards.Create(ctx, "a", "upload", Edit{Data: []byte(`{"name": "A"}`), AvatarBlob: kept.SHA256, Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.pool.Exec(ctx, `UPDATE blob SET created_at = now() - interval '2 days' WHERE sha256 <> $1`, recent.SHA256); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.MkdirAll(lib.blobs.TmpDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	stale, fresh := filepath.Join(lib.blobs.TmpDir(), "stale"), filepath.Join(lib.blobs.TmpDir(), "fresh")
	for _, f := range []string{stale, fresh} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	blobs, leftovers, err := lib.CollectGarbage(ctx, 24*time.Hour)
	if err != nil || blobs != 1 || leftovers != 1 {
		t.Fatalf("CollectGarbage = %d, %d, %v; want 1, 1", blobs, leftovers, err)
	}
	if _, err := lib.Blob(ctx, orphan.SHA256); err != ErrNotFound {
		t.Errorf("orphan blob row: %v", err)
	}
	if p, _ := lib.blobs.Path(orphan.SHA256); fileExists(p) {
		t.Error("orphan blob file still exists")
	}
	for _, b := range []Blob{kept, recent} {
		if p, _ := lib.blobs.Path(b.SHA256); !fileExists(p) {
			t.Errorf("blob %s was deleted", b.SHA256)
		}
	}
	if fileExists(stale) || !fileExists(fresh) {
		t.Errorf("tmp: stale exists %v, fresh exists %v", fileExists(stale), fileExists(fresh))
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestCollectGarbageKeepsReusedBlob(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	b, err := lib.PutBlob(ctx, strings.NewReader("avatar"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.pool.Exec(ctx, `UPDATE blob SET created_at = now() - interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	// Stored again by a new import that hasn't referenced it yet.
	if _, err := lib.PutBlob(ctx, strings.NewReader("avatar")); err != nil {
		t.Fatal(err)
	}
	if n, _, err := lib.CollectGarbage(ctx, 24*time.Hour); err != nil || n != 0 {
		t.Fatalf("CollectGarbage = %d, %v; want 0", n, err)
	}
	if _, err := lib.Blob(ctx, b.SHA256); err != nil {
		t.Fatalf("reused blob: %v", err)
	}
}

func TestCollectGarbageWaitsForRestore(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	tx, err := lib.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('innkeeper-restore'))`); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if _, _, err := lib.CollectGarbage(short, time.Hour); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it to wait for the lock", err)
	}
	tx.Rollback(ctx)
	if _, _, err := lib.CollectGarbage(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
}
