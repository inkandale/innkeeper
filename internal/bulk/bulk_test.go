package bulk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/testdb"
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
		return map[string]jobs.Handler{ArchiveKind: Archive(lib, nil)}
	}, "a", "b")
	parent, err := EnqueueArchive(ctx, q, ids, true, false)
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
	head, _ := lib.Cards.Get(ctx, ids[0])
	if _, err := lib.Cards.Update(ctx, ids[0], head.HeadRevisionID,
		library.Edit{Data: []byte(`{"name": "x", "tags": ["old", "keep"]}`), Source: "ui"}); err != nil {
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
		c, err := lib.Cards.Get(ctx, ids[i])
		if err != nil {
			t.Fatal(err)
		}
		rev, err := lib.Cards.Revision(ctx, c.HeadRevisionID)
		if err != nil {
			t.Fatal(err)
		}
		var data struct{ Tags []string }
		if err := json.Unmarshal(rev.Data, &data); err != nil || !slices.Equal(data.Tags, want) {
			t.Errorf("card %d tags = %v, want %v (%v)", ids[i], data.Tags, want, err)
		}
	}

	p, err = EnqueueCollect(ctx, q, ids, col.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, q, p.ID)
	for _, id := range ids {
		if got, _ := lib.CardCollections(ctx, id); !slices.Equal(got, []int64{col.ID}) {
			t.Fatalf("card %d collections = %v", id, got)
		}
	}
	p, err = EnqueueCollect(ctx, q, ids[:1], col.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, q, p.ID)
	for i, want := range [][]int64{nil, {col.ID}} {
		if got, _ := lib.CardCollections(ctx, ids[i]); !slices.Equal(got, want) {
			t.Fatalf("after uncollect, card %d collections = %v", ids[i], got)
		}
	}
}

func TestArchiveLorebooks(t *testing.T) {
	ctx := context.Background()
	lib, q, _ := setup(t, func(lib *library.Library) map[string]jobs.Handler {
		return map[string]jobs.Handler{LorebookArchiveKind: ArchiveLorebook(lib, nil)}
	})
	var ids []int64
	for _, slug := range []string{"a", "b"} {
		l, _, err := lib.Lorebooks.Create(ctx, slug, "upload", library.Edit{Data: []byte(`{"name": "x", "entries": []}`), Source: "ui"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, l.ID)
	}
	parent, err := EnqueueLorebookArchive(ctx, q, ids, true, false)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, q, parent.ID)
	for _, id := range ids {
		if l, _ := lib.Lorebooks.Get(ctx, id); l.ArchivedAt == nil {
			t.Errorf("lorebook %d not archived", id)
		}
	}
}

// appDown is an app whose deletes fail.
type appDown struct{}

func (appDown) Unpublish(context.Context, *library.Kind, int64) error {
	return errors.New("app unavailable")
}

func TestArchiveStaysUnarchivedWhenUnpublishFails(t *testing.T) {
	ctx := context.Background()
	lib, _, ids := setup(t, func(*library.Library) map[string]jobs.Handler { return nil }, "a")
	if err := SetArchived(ctx, lib, appDown{}, library.Card, ids[0], true, true); err == nil {
		t.Fatal("archive worked; want the app's error")
	}
	if c, _ := lib.Cards.Get(ctx, ids[0]); c.ArchivedAt != nil {
		t.Fatal("card archived though its app copy wasn't deleted")
	}
	if err := SetArchived(ctx, lib, appDown{}, library.Card, ids[0], true, false); err != nil {
		t.Fatalf("archive without unpublish: %v", err)
	}
}
