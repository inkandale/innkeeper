package bulk

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/testdb"
)

func TestArchive(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	lib := library.New(pool, &blobstore.Store{Name: "library", Root: t.TempDir()})
	var ids []int64
	for _, slug := range []string{"a", "b"} {
		c, _, err := lib.Cards.Create(ctx, slug, "upload", library.Edit{Data: []byte(`{"name": "x"}`), Source: "ui"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	q := jobs.New(pool)
	w := &jobs.Worker{Queue: q, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: map[string]jobs.Handler{ArchiveKind: Archive(lib)}}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(wctx) }()
	defer func() { cancel(); <-done }()

	parent, err := EnqueueArchive(ctx, q, ids, true)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		j, _ := q.Get(ctx, parent.ID)
		if j.Status == jobs.StatusSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bulk job still %s", j.Status)
		}
	}
	for _, id := range ids {
		if c, _ := lib.Cards.Get(ctx, id); c.ArchivedAt == nil {
			t.Errorf("card %d not archived", id)
		}
	}
}
