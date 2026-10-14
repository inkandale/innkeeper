package importer

import (
	"context"
	"errors"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

func (e *env) sources(t *testing.T, card int64) []library.Source {
	t.Helper()
	got, err := e.lib.Cards.Sources(context.Background(), card)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The fake imports and detects in one process, as url-import does: emit.card
// must not wait on a sites.detect that process is busy answering.
func TestImportDetectsThroughTheImportingPlugin(t *testing.T) {
	e := setupCaps(t, []string{"import", "sites"})
	j, p := e.run(t, `{"cards": 3, "source": true}`)
	if j.Status != jobs.StatusSucceeded || p.Imported != 3 {
		t.Fatalf("job %s, progress %+v: %s", j.Status, p, errText(j))
	}
	var detect int64
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM job WHERE kind = $1`, DetectKind).Scan(&detect); err != nil {
		t.Fatalf("no detect job after the import: %v", err)
	}
	if j := e.wait(t, detect); j.Status != jobs.StatusSucceeded {
		t.Fatalf("detect job %s: %s", j.Status, errText(j))
	}
	got := e.sources(t, e.slugID(t, "card", "fake-1"))
	if len(got) != 1 || got[0].Kind != "fake" || got[0].Ref != "u/card-1" || got[0].LinkedBy != "auto" {
		t.Fatalf("sources = %+v", got)
	}
	if n := e.count(t, "source_detect"); n != 0 {
		t.Fatalf("%d entities still queued", n)
	}
}

func TestDetectKeepsAnEntityQueuedOnARetryableFailure(t *testing.T) {
	ctx := context.Background()
	e := setupCaps(t, []string{"import", "sites"})
	if err := e.reg.UpdateInstance(ctx, e.inst, plugins.InstanceEdit{Config: map[string]any{"sites_fail": "retryable"}}); err != nil {
		t.Fatal(err)
	}
	id, _, err := e.lib.Cards.Import(ctx, "fake:x", "X", library.Edit{Data: []byte(`{"name": "X", "source": ["https://fake.example/u/x"]}`), Source: "plugin:fake"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.im.RunDetect(ctx, &jobs.Run{}); !errors.Is(err, jobs.ErrRetry) {
		t.Fatalf("run = %v, want a retry", err)
	}
	if e.count(t, "source_detect") != 1 || len(e.sources(t, id)) != 0 {
		t.Fatal("a failed detection left the queue")
	}

	if err := e.reg.UpdateInstance(ctx, e.inst, plugins.InstanceEdit{Config: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if err := e.im.RunDetect(ctx, &jobs.Run{}); err != nil {
		t.Fatal(err)
	}
	if got := e.sources(t, id); e.count(t, "source_detect") != 0 || len(got) != 1 || got[0].Ref != "u/x" {
		t.Fatalf("after the retry: sources %+v, %d queued", got, e.count(t, "source_detect"))
	}
}

func TestDetectKeepsTheQueueWithoutASitesPlugin(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	id, _, err := e.lib.Cards.Import(ctx, "fake:x", "X", library.Edit{Data: []byte(`{"name": "X", "extensions": {"chub": {"full_path": "u/x"}}}`), Source: "plugin:fake"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.im.RunDetect(ctx, &jobs.Run{}); err != nil {
		t.Fatal(err)
	}
	if got := e.sources(t, id); len(got) != 0 || e.count(t, "source_detect") != 1 {
		t.Fatalf("sources = %+v, %d queued; want none and the entity still queued", got, e.count(t, "source_detect"))
	}
}
