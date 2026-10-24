package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

type env struct {
	pool *pgxpool.Pool
	lib  *library.Library
	reg  *plugins.Registry
	q    *jobs.Queue
	run  *Runner
	inst int64
}

// newEnv has one fake ai instance offering ai.embed, not yet the embed instance.
func newEnv(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"ai"}))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := plugins.New(pool, map[string]*protocol.Manifest{"fake": m}, store, box)
	mgr := pluginhost.NewManager(nil, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(mgr.Close)
	inst, err := reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: new("Embed"),
		Config: map[string]any{"services": []string{protocol.MethodAIEmbed}}})
	if err != nil {
		t.Fatal(err)
	}
	lib := library.New(pool, store)
	return env{pool: pool, lib: lib, reg: reg, q: jobs.New(pool), run: &Runner{Lib: lib, Registry: reg, Plugins: mgr}, inst: inst}
}

func (e env) card(t *testing.T, slug, data string) (int64, int64) {
	t.Helper()
	c, rev, err := e.lib.Cards.Create(context.Background(), library.DefaultLibrary, slug, "upload",
		library.Edit{Data: json.RawMessage(data), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	return c.ID, rev.ID
}

func TestEmbedJobDoesNothingWithoutEmbedInstance(t *testing.T) {
	e := newEnv(t)
	e.card(t, "brakka", `{"name": "Brakka"}`)
	if err := e.run.Run(context.Background(), &jobs.Run{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.lib.Unembedded(context.Background(), e.inst, 0, 0, 10); len(got) != 1 {
		t.Fatalf("a run without an embed instance embedded the card: %+v", got)
	}
}

func TestEmbedJobEmbedsEveryCardInBatches(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := range batchSize + 3 {
		e.card(t, "card-"+strconv.Itoa(i), `{"name": "Card `+strconv.Itoa(i)+`"}`)
	}
	if err := e.reg.SetSetting(ctx, InstanceKey, strconv.FormatInt(e.inst, 10)); err != nil {
		t.Fatal(err)
	}
	r := &jobs.Run{}
	if err := e.run.Run(ctx, r); err != nil {
		t.Fatal(err)
	}
	if got, err := e.lib.Unembedded(ctx, e.inst, 0, 0, 100); err != nil || len(got) != 0 {
		t.Fatalf("left unembedded: %d, %v", len(got), err)
	}
}

func TestEmbedJobReembedsEditedCard(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, rev := e.card(t, "brakka", `{"name": "Brakka"}`)
	if err := e.reg.SetSetting(ctx, InstanceKey, strconv.FormatInt(e.inst, 10)); err != nil {
		t.Fatal(err)
	}
	if err := e.run.Run(ctx, &jobs.Run{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.lib.Cards.Update(ctx, id, rev, library.Edit{Data: json.RawMessage(`{"name": "Brakka the Bold"}`), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.lib.Unembedded(ctx, e.inst, 0, 0, 10); len(got) != 1 {
		t.Fatalf("an edited card isn't due: %+v", got)
	}
	if err := e.run.Run(ctx, &jobs.Run{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.lib.Unembedded(ctx, e.inst, 0, 0, 10); len(got) != 0 {
		t.Fatalf("still due after a run: %+v", got)
	}
}

func TestEmbedJobIgnoresADeletedEmbedInstance(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.card(t, "brakka", `{"name": "Brakka"}`)
	if err := e.reg.SetSetting(ctx, InstanceKey, "999999"); err != nil {
		t.Fatal(err)
	}
	if err := e.run.Run(ctx, &jobs.Run{}); err != nil {
		t.Fatalf("a run naming a missing instance: %v, want nil", err)
	}
}

// warnings are the warn lines job j logged.
func (e env) warnings(t *testing.T, j jobs.Job) []string {
	t.Helper()
	logs, err := e.q.Logs(context.Background(), j.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range logs {
		if l.Level == "warn" {
			out = append(out, l.Message)
		}
	}
	return out
}

// runJob runs an embed job on a real worker, so the run can log, and
// returns the finished job.
func (e env) runJob(t *testing.T) jobs.Job {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w := &jobs.Worker{Queue: e.q, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{Kind: e.run.Run}}
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("worker: %v", err)
		}
	}()
	j, err := e.q.Enqueue(ctx, jobs.Spec{Kind: Kind})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if j, err = e.q.Get(ctx, j.ID); err != nil {
			t.Fatal(err)
		}
		if j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed {
			return j
		}
	}
	t.Fatalf("job %d still running", j.ID)
	return j
}

func TestEmbedJobSkipsACardTheModelRejects(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	poison, _ := e.card(t, "poison", `{"name": "POISON"}`)
	for i := range batchSize + 3 {
		e.card(t, "card-"+strconv.Itoa(i), `{"name": "Card `+strconv.Itoa(i)+`"}`)
	}
	if err := e.reg.SetSetting(ctx, InstanceKey, strconv.FormatInt(e.inst, 10)); err != nil {
		t.Fatal(err)
	}
	j := e.runJob(t)
	if j.Status != jobs.StatusSucceeded {
		t.Fatalf("job %s: %s", j.Status, *j.Error)
	}
	got, err := e.lib.Unembedded(ctx, e.inst, 0, 0, 100)
	if err != nil || len(got) != 1 || got[0].CardID != poison {
		t.Fatalf("left unembedded: %+v, %v; want only the rejected card", got, err)
	}
	if warnings := e.warnings(t, j); len(warnings) != 1 || !strings.Contains(warnings[0], fmt.Sprintf("card %d ", poison)) {
		t.Fatalf("warnings = %q; want one naming card %d", warnings, poison)
	}
}

func TestEmbedJobIgnoresADisabledEmbedInstance(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.card(t, "brakka", `{"name": "Brakka"}`)
	if err := e.reg.SetSetting(ctx, InstanceKey, strconv.FormatInt(e.inst, 10)); err != nil {
		t.Fatal(err)
	}
	if err := e.reg.UpdateInstance(ctx, e.inst, plugins.InstanceEdit{Disabled: new(true)}); err != nil {
		t.Fatal(err)
	}
	if err := e.run.Run(ctx, &jobs.Run{}); err != nil {
		t.Fatalf("a run with a disabled instance: %v, want nil", err)
	}
	if got, _ := e.lib.Unembedded(ctx, e.inst, 0, 0, 10); len(got) != 1 {
		t.Fatalf("a disabled instance embedded the card: %+v", got)
	}
}

func TestEmbedJobFailsWhenTheModelRejectsEveryCard(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.card(t, "poison-1", `{"name": "POISON one"}`)
	e.card(t, "poison-2", `{"name": "POISON two"}`)
	if err := e.reg.SetSetting(ctx, InstanceKey, strconv.FormatInt(e.inst, 10)); err != nil {
		t.Fatal(err)
	}
	if j := e.runJob(t); j.Status != jobs.StatusFailed {
		t.Fatalf("job %s; want failed when every card is rejected", j.Status)
	}
}

func TestEmbedJobFailsWithABrokenEmbedInstance(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.card(t, "brakka", `{"name": "Brakka"}`)
	if err := e.reg.SetSetting(ctx, InstanceKey, strconv.FormatInt(e.inst, 10)); err != nil {
		t.Fatal(err)
	}
	inst, err := e.reg.Instance(ctx, e.inst)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.reg.MarkBroken(ctx, e.inst, inst.Version, errors.New("won't start")); err != nil {
		t.Fatal(err)
	}
	if j := e.runJob(t); j.Status != jobs.StatusFailed {
		t.Fatalf("job %s; want failed with a broken instance", j.Status)
	}
}

func TestEmbedJobSwitchesToANewEmbedInstanceMidRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := range batchSize + 3 {
		e.card(t, "card-"+strconv.Itoa(i), `{"name": "Card `+strconv.Itoa(i)+`"}`)
	}
	next, err := e.reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: new("Next"),
		Config: map[string]any{"services": []string{protocol.MethodAIEmbed}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.reg.SetSetting(ctx, InstanceKey, strconv.FormatInt(e.inst, 10)); err != nil {
		t.Fatal(err)
	}
	// The owner picks the next instance as soon as the first batch is saved.
	if _, err := e.pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION switch_embed_instance() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN UPDATE setting SET value = '%d' WHERE key = '%s'; RETURN NULL; END $$;
		CREATE TRIGGER switch_embed_instance AFTER INSERT ON card_embedding
		FOR EACH STATEMENT EXECUTE FUNCTION switch_embed_instance()`, next, InstanceKey)); err != nil {
		t.Fatal(err)
	}
	if j := e.runJob(t); j.Status != jobs.StatusSucceeded {
		t.Fatalf("job %s: %s", j.Status, *j.Error)
	}
	if got, err := e.lib.Unembedded(ctx, next, 0, 0, 100); err != nil || len(got) != 0 {
		t.Fatalf("left unembedded by the new instance: %d, %v", len(got), err)
	}
}

func TestEmbedJobFailsAtAnInstanceFaultWithoutTryingEachCard(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.card(t, "denied-1", `{"name": "DENIED one"}`)
	e.card(t, "denied-2", `{"name": "DENIED two"}`)
	if err := e.reg.SetSetting(ctx, InstanceKey, strconv.FormatInt(e.inst, 10)); err != nil {
		t.Fatal(err)
	}
	j := e.runJob(t)
	if j.Status != jobs.StatusFailed {
		t.Fatalf("job %s; want failed when the instance refuses every call", j.Status)
	}
	if warnings := e.warnings(t, j); len(warnings) != 0 {
		t.Fatalf("warnings = %q; want none, since no card is at fault", warnings)
	}
}

func TestEmbedJobForALibraryEmbedsOnlyItsCards(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.card(t, "home", `{"name": "Home"}`)
	archive, err := e.lib.CreateLibrary(ctx, "Archive")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.lib.Cards.Create(ctx, archive.ID, "away", "upload",
		library.Edit{Data: json.RawMessage(`{"name": "Away"}`), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	if err := e.reg.SetSetting(ctx, InstanceKey, strconv.FormatInt(e.inst, 10)); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(Payload{Library: archive.ID})
	if err := e.run.Run(ctx, &jobs.Run{Job: jobs.Job{Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	if got, err := e.lib.Unembedded(ctx, e.inst, archive.ID, 0, 10); err != nil || len(got) != 0 {
		t.Fatalf("left unembedded in the library: %+v, %v", got, err)
	}
	if got, err := e.lib.Unembedded(ctx, e.inst, library.DefaultLibrary, 0, 10); err != nil || len(got) != 1 {
		t.Fatalf("unembedded in another library = %+v, %v; want its one card left alone", got, err)
	}
}
