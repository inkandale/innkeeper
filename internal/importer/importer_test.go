package importer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/testdb"
	"github.com/davidgibbons/innkeeper/internal/testutil"
	"github.com/davidgibbons/innkeeper/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type env struct {
	pool *pgxpool.Pool
	q    *jobs.Queue
	lib  *library.Library
	reg  *plugins.Registry
	inst int64
}

// setup runs a worker with the importer against a fake import plugin started
// with args.
func setup(t *testing.T, args ...string) *env {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	lib := library.New(pool, store)
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"import"}, args...))
	if err != nil {
		t.Fatal(err)
	}
	reg := plugins.New(pool, map[string]*protocol.Manifest{"fake": m}, store)
	im := New(lib, reg, quiet)
	mgr := pluginhost.NewManager(im.Handle, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	im.Plugins = mgr

	q := jobs.New(pool)
	w := &jobs.Worker{Queue: q, Log: quiet, Poll: 50 * time.Millisecond,
		Backoff: func(int) time.Duration { return 0 }, Handlers: map[string]jobs.Handler{Kind: im.Run}}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(wctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("worker: %v", err)
		}
	})

	inst, err := reg.CreateInstance(ctx, "fake", "Fake", nil)
	if err != nil {
		t.Fatal(err)
	}
	return &env{pool: pool, q: q, lib: lib, reg: reg, inst: inst}
}

// run imports with params and returns the finished job and its progress.
func (e *env) run(t *testing.T, params string) (jobs.Job, progress) {
	t.Helper()
	ctx := context.Background()
	j, err := e.q.Enqueue(ctx, jobs.Spec{Kind: Kind, InstanceID: &e.inst, Payload: Payload{Params: json.RawMessage(params)}})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if j, err = e.q.Get(ctx, j.ID); err != nil {
			t.Fatal(err)
		}
		if j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed {
			var p progress
			if err := json.Unmarshal(j.Progress, &p); err != nil {
				t.Fatal(err)
			}
			return j, p
		}
	}
	t.Fatalf("import job still %s", j.Status)
	return j, progress{}
}

func errText(j jobs.Job) string {
	if j.Error == nil {
		return ""
	}
	return *j.Error
}

func (e *env) count(t *testing.T, table string) (n int) {
	t.Helper()
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImportCards(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	j, p := e.run(t, `{"cards": 3, "book": true, "avatar": true, "lorebooks": 1}`)
	if j.Status != jobs.StatusSucceeded || p.Imported != 4 || p.Done != 3 || p.Total != 3 {
		t.Fatalf("job %s, progress %+v", j.Status, p)
	}
	if n := e.count(t, "card"); n != 3 {
		t.Fatalf("%d cards, want 3", n)
	}
	if n := e.count(t, "lorebook"); n != 4 {
		t.Fatalf("%d lorebooks, want 3 split from cards and 1 standalone", n)
	}
	if _, err := e.lib.Lorebooks.GetBySlug(ctx, "world-0"); err != nil {
		t.Fatalf("standalone lorebook: %v", err)
	}

	c, err := e.lib.Cards.GetBySlug(ctx, "fake-1")
	if err != nil {
		t.Fatal(err)
	}
	rev, err := e.lib.Cards.Revision(ctx, c.HeadRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Provenance != "fake:card-1" || rev.Source != "plugin:fake" || rev.AvatarBlob == nil {
		t.Fatalf("card %+v, revision %+v", c, rev)
	}
	books, err := e.lib.CardLorebooks(ctx, c.ID)
	if err != nil || len(books) != 1 {
		t.Fatalf("linked lorebooks = %v, %v", books, err)
	}
	book, _ := e.lib.Lorebooks.Get(ctx, books[0])
	if book.Slug != "lore-1" {
		t.Fatalf("linked lorebook slug = %q, want lore-1", book.Slug)
	}

	// Importing the same items again adds nothing.
	_, p = e.run(t, `{"cards": 3, "book": true, "avatar": true, "lorebooks": 1}`)
	if p.Imported != 0 || p.Skipped != 4 || e.count(t, "card") != 3 {
		t.Fatalf("re-import progress %+v", p)
	}
}

func TestImportResumesFromCheckpointAfterCrash(t *testing.T) {
	e := setup(t)
	j, p := e.run(t, `{"cards": 5, "crash_at": 2}`)
	if j.Status != jobs.StatusSucceeded || j.Attempts != 2 {
		t.Fatalf("job %s after %d attempts: %s", j.Status, j.Attempts, errText(j))
	}
	// Only card 2, sent after the last checkpoint, is sent again.
	if p.Skipped != 1 || e.count(t, "card") != 5 {
		t.Fatalf("progress %+v, %d cards", p, e.count(t, "card"))
	}
}

func TestBadCardFailsOnlyThatItem(t *testing.T) {
	e := setup(t)
	j, p := e.run(t, `{"cards": 3, "bad": 1}`)
	if j.Status != jobs.StatusSucceeded || p.Imported != 2 || p.Failed != 1 {
		t.Fatalf("job %s, progress %+v", j.Status, p)
	}
	if len(p.Errors) != 1 || !strings.HasPrefix(p.Errors[0], `card-1: spec "chara_card_v2"`) {
		t.Fatalf("errors = %q", p.Errors)
	}
}

func TestPluginErrors(t *testing.T) {
	e := setup(t)
	j, _ := e.run(t, `{"fail": "fatal"}`)
	if j.Status != jobs.StatusFailed || j.Attempts != 1 || !strings.Contains(errText(j), "no such folder") {
		t.Fatalf("fatal: %s after %d attempts: %s", j.Status, j.Attempts, errText(j))
	}
	j, _ = e.run(t, `{"fail": "retryable"}`)
	if j.Status != jobs.StatusFailed || j.Attempts != j.MaxAttempts || !strings.Contains(errText(j), "source unavailable") {
		t.Fatalf("retryable: %s after %d attempts: %s", j.Status, j.Attempts, errText(j))
	}
}

func TestPluginThatFailsToStartBreaksInstance(t *testing.T) {
	e := setup(t, "-mode=wrong-version")
	j, _ := e.run(t, `{}`)
	if j.Status != jobs.StatusFailed || j.Attempts != 1 {
		t.Fatalf("job %s after %d attempts", j.Status, j.Attempts)
	}
	if _, err := e.reg.Instance(context.Background(), e.inst); !errors.Is(err, plugins.ErrBroken) {
		t.Fatalf("instance: %v, want ErrBroken", err)
	}
}
