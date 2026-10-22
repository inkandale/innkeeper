package importer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/secrets"
	iservices "github.com/inkandale/innkeeper/internal/services"
	"github.com/inkandale/innkeeper/internal/targets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type env struct {
	pool *pgxpool.Pool
	q    *jobs.Queue
	lib  *library.Library
	reg  *plugins.Registry
	im   *Importer
	inst int64
}

// setup runs a worker with the importer against a fake import plugin started
// with args.
func setup(t *testing.T, args ...string) *env {
	t.Helper()
	return setupCaps(t, []string{"import"}, args...)
}

// setupCaps is setup with a plugin that has caps.
func setupCaps(t *testing.T, caps []string, args ...string) *env {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	lib := library.New(pool, store)
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, caps, args...))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := plugins.New(pool, map[string]*protocol.Manifest{"fake": m}, store, box)
	q := jobs.New(pool)
	runs := pluginhost.NewRuns(quiet)
	// As the worker wires it, so plugin calls go through the real broker.
	broker := &iservices.Broker{Registry: reg, Runs: runs}
	mgr := pluginhost.NewManager(broker.Handle(runs.Handle), time.Minute, quiet)
	broker.Plugins = mgr
	t.Cleanup(mgr.Close)
	im := New(lib, reg, q, mgr, runs, targets.New(pool, lib, reg, mgr, quiet), quiet)

	w := &jobs.Worker{Queue: q, Log: quiet, Poll: 50 * time.Millisecond,
		Backoff: func(int) time.Duration { return 0 }, Handlers: map[string]jobs.Handler{Kind: im.Run, DetectKind: im.RunDetect}}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(wctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("worker: %v", err)
		}
	})

	inst, err := reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: new("Fake")})
	if err != nil {
		t.Fatal(err)
	}
	return &env{pool: pool, q: q, lib: lib, reg: reg, im: im, inst: inst}
}

// run imports with params and returns the finished job and its progress.
func (e *env) run(t *testing.T, params string) (jobs.Job, progress) {
	t.Helper()
	return e.runPayload(t, Payload{Params: json.RawMessage(params)})
}

func (e *env) runPayload(t *testing.T, payload Payload) (jobs.Job, progress) {
	t.Helper()
	ctx := context.Background()
	j, err := e.q.Enqueue(ctx, jobs.Spec{Kind: Kind, InstanceID: &e.inst, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	j = e.wait(t, j.ID)
	var p progress
	if err := json.Unmarshal(j.Progress, &p); err != nil {
		t.Fatal(err)
	}
	return j, p
}

// wait returns the job once it finishes.
func (e *env) wait(t *testing.T, id int64) jobs.Job {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		j, err := e.q.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed {
			return j
		}
	}
	t.Fatalf("job %d still running", id)
	return jobs.Job{}
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

func (e *env) slugID(t *testing.T, table, slug string) (id int64) {
	t.Helper()
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM `+table+` WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatalf("%s %s: %v", table, slug, err)
	}
	return id
}

func TestImportCards(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	j, p := e.run(t, `{"cards": 3, "book": true, "avatar": true, "lorebooks": 1}`)
	if j.Status != jobs.StatusSucceeded || p.Imported != 4 || p.Done != 3 || p.Total != 3 || j.CardsChanged != 3 {
		t.Fatalf("job %s, %d cards changed, progress %+v", j.Status, j.CardsChanged, p)
	}
	if n := e.count(t, "card"); n != 3 {
		t.Fatalf("%d cards, want 3", n)
	}
	if n := e.count(t, "lorebook"); n != 4 {
		t.Fatalf("%d lorebooks, want 3 split from cards and 1 standalone", n)
	}
	e.slugID(t, "lorebook", "world-0")

	c, err := e.lib.Cards.Get(ctx, e.slugID(t, "card", "fake-1"))
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
	j, p = e.run(t, `{"cards": 3, "book": true, "avatar": true, "lorebooks": 1}`)
	if p.Imported != 0 || p.Skipped != 4 || e.count(t, "card") != 3 || j.CardsChanged != 0 {
		t.Fatalf("re-import: %d cards changed, progress %+v", j.CardsChanged, p)
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
	if len(p.Errors) != 1 || !strings.HasPrefix(p.Errors[0], `card-1: invalid data: spec "chara_card_v2"`) {
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

func TestCancelledStartLeavesInstanceWorking(t *testing.T) {
	e := setup(t, "-mode=slow-init")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r := &jobs.Run{Job: jobs.Job{ID: 1, Kind: Kind, Attempts: 1, InstanceID: &e.inst, Payload: json.RawMessage(`{}`)}}
	if err := e.im.Run(ctx, r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("run = %v, want deadline exceeded", err)
	}
	if _, err := e.reg.Instance(context.Background(), e.inst); err != nil {
		t.Fatalf("instance: %v", err)
	}
}

// hideJobs returns the payloads of the queued hide jobs.
func (e *env) hideJobs(t *testing.T) []bulk.HidePayload {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT payload FROM job WHERE kind = $1 ORDER BY id`, bulk.HideKind)
	if err != nil {
		t.Fatal(err)
	}
	raws, err := pgx.CollectRows(rows, pgx.RowTo[json.RawMessage])
	if err != nil {
		t.Fatal(err)
	}
	out := make([]bulk.HidePayload, len(raws))
	for i, raw := range raws {
		if err := json.Unmarshal(raw, &out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestImportQueuesHideWhenLibraryHidesDuplicates(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if err := e.lib.SetLibrarySetting(ctx, library.DefaultLibrary, "duplicates", `"near"`); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetLibrarySetting(ctx, library.DefaultLibrary, "duplicate_keep", `"oldest"`); err != nil {
		t.Fatal(err)
	}
	e.run(t, `{"cards": 2}`)
	got := e.hideJobs(t)
	want := []bulk.HidePayload{{Library: library.DefaultLibrary, Duplicates: "near", Distance: 3, Keep: "oldest"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hide jobs = %+v, want %+v", got, want)
	}

	// Importing the same cards again changes none, so nothing more is queued.
	e.run(t, `{"cards": 2}`)
	if got := e.hideJobs(t); len(got) != 1 {
		t.Fatalf("re-import queued hide jobs %+v", got)
	}
}

func TestImportQueuesNoHideWithDuplicatesOff(t *testing.T) {
	e := setup(t)
	e.run(t, `{"cards": 2}`)
	if got := e.hideJobs(t); len(got) != 0 {
		t.Fatalf("hide jobs = %+v, want none", got)
	}
}
