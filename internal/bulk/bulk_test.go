package bulk

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/testdb"
)

// setup returns a library with one card per slug, and a queue whose worker
// runs handlers until the test ends.
func setup(t *testing.T, handlers func(*library.Library) map[string]jobs.Handler, slugs ...string) (*library.Library, *jobs.Queue, []int64) {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	lib := library.New(pool, &blobstore.Store{Name: "library", Root: t.TempDir()})
	var ids []int64
	for _, slug := range slugs {
		c, _, err := lib.Cards.Create(ctx, slug, "upload", library.Edit{Data: []byte(`{"name": "x"}`), Source: "ui"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	q := jobs.New(pool)
	w := &jobs.Worker{Queue: q, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Handlers: handlers(lib)}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(wctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return lib, q, ids
}

// wait returns the job once it succeeds, and fails the test if it fails or
// takes over 10 seconds.
func wait(t *testing.T, q *jobs.Queue, id int64) jobs.Job {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		j, _ := q.Get(context.Background(), id)
		if j.Status == jobs.StatusSucceeded {
			return j
		}
		if j.Status == jobs.StatusFailed || time.Now().After(deadline) {
			msg := ""
			if j.Error != nil {
				msg = *j.Error
			}
			t.Fatalf("job %d is %s: %s", id, j.Status, msg)
		}
	}
}

func TestArchive(t *testing.T) {
	ctx := context.Background()
	lib, q, ids := setup(t, func(lib *library.Library) map[string]jobs.Handler {
		return map[string]jobs.Handler{ArchiveKind: Archive(lib)}
	}, "a", "b")
	parent, err := EnqueueArchive(ctx, q, ids, true)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, q, parent.ID)
	for _, id := range ids {
		if c, _ := lib.Cards.Get(ctx, id); c.ArchivedAt == nil {
			t.Errorf("card %d not archived", id)
		}
	}
}

func TestTagAndCollect(t *testing.T) {
	ctx := context.Background()
	lib, q, ids := setup(t, func(lib *library.Library) map[string]jobs.Handler {
		return map[string]jobs.Handler{TagKind: Tag(lib), CollectKind: Collect(lib)}
	}, "a", "b")
	if err := lib.SetCardTags(ctx, ids[0], []string{"old", "keep"}); err != nil {
		t.Fatal(err)
	}
	col, err := lib.CreateCollection(ctx, "Fantasy", "")
	if err != nil {
		t.Fatal(err)
	}

	p, err := EnqueueTag(ctx, q, ids, []string{"new"}, []string{"old"})
	if err != nil {
		t.Fatal(err)
	}
	wait(t, q, p.ID)
	for i, want := range [][]string{{"keep", "new"}, {"new"}} {
		if got, _ := lib.CardTags(ctx, ids[i]); !slices.Equal(got, want) {
			t.Errorf("card %d tags = %v, want %v", ids[i], got, want)
		}
	}

	p, err = EnqueueCollect(ctx, q, ids, col.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, q, p.ID)
	if got, _ := lib.CollectionCards(ctx, col.ID); len(got) != 2 {
		t.Fatalf("collected = %v", got)
	}
	p, err = EnqueueCollect(ctx, q, ids[:1], col.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, q, p.ID)
	if got, _ := lib.CollectionCards(ctx, col.ID); !slices.Equal(got, ids[1:]) {
		t.Fatalf("after uncollect = %v", got)
	}
}
