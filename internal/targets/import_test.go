package targets

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
)

// appCard puts a card in the fake app, as if the owner made it there.
func (e *env) appCard(id string, data map[string]any, extra map[string]any) {
	e.t.Helper()
	item := map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data}
	for k, v := range extra {
		item[k] = v
	}
	e.writeRemote(id, item)
}

// runImport imports from the fake app and returns the job's outcome counts.
func (e *env) runImport(remoteIDs []string) map[string]int {
	e.t.Helper()
	q := jobs.New(e.pool)
	wait := e.startWorker(q)
	parent, err := e.s.EnqueueImport(e.ctx, q, e.inst, remoteIDs)
	if err != nil {
		e.t.Fatal(err)
	}
	if j := wait(parent.ID); j.Status != jobs.StatusSucceeded {
		e.t.Fatalf("import %s: %v", j.Status, j.Error)
	}
	out, err := q.ChildOutcomes(e.ctx, parent.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// bindingOf returns the binding that uses the app item.
func (e *env) bindingOf(remoteID string) Binding {
	e.t.Helper()
	var id int64
	if err := e.pool.QueryRow(e.ctx, `SELECT id FROM binding WHERE instance_id = $1 AND remote_id = $2`,
		e.inst, remoteID).Scan(&id); err != nil {
		e.t.Fatalf("binding for %s: %v", remoteID, err)
	}
	b, err := e.s.Binding(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func pngFile(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "iko.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportEmbedded(t *testing.T) {
	e := setup(t, nil)
	e.appCard("r1", map[string]any{"name": "Iko", "description": "A courier.", "first_mes": "Package!",
		"character_book": map[string]any{"name": "Iko lore", "entries": []any{}}},
		map[string]any{"avatar": pngFile(t)})
	if out := e.runImport(nil); out[OutcomeCreated] != 1 || len(out) != 1 {
		t.Fatalf("outcomes = %v", out)
	}
	b := e.bindingOf("r1")
	if b.Status != "in_sync" || b.CardID == e.card {
		t.Fatalf("binding = %+v", b)
	}
	if e.head(b.CardID).AvatarBlob == nil {
		t.Error("imported card has no avatar")
	}
	if lbs, err := e.lib.CardLorebooks(e.ctx, b.CardID); err != nil || len(lbs) != 1 {
		t.Errorf("card lorebooks = %v, %v", lbs, err)
	}
	// Everything is bound now, so another import has nothing to do.
	if out := e.runImport(nil); len(out) != 0 {
		t.Fatalf("second import outcomes = %v", out)
	}
}

func TestImportStandalone(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	e.writeRemote("l1", map[string]any{"lorebook": map[string]any{"name": "Forge lore", "description": "Smithing.",
		"entries": []any{}}})
	for id, name := range map[string]string{"r1": "Iko", "r2": "Tam"} {
		e.appCard(id, map[string]any{"name": name, "description": "A courier.", "first_mes": "Hi."},
			map[string]any{"lorebook_remote_ids": []string{"l1"}})
	}
	if out := e.runImport(nil); out[OutcomeCreated] != 3 || len(out) != 1 {
		t.Fatalf("outcomes = %v", out)
	}
	lb := e.bindingOf("l1")
	if lb.Status != "in_sync" {
		t.Fatalf("lorebook binding = %+v", lb)
	}
	for _, id := range []string{"r1", "r2"} {
		b := e.bindingOf(id)
		lbs, err := e.lib.CardLorebooks(e.ctx, b.CardID)
		if b.Status != "in_sync" || err != nil || len(lbs) != 1 || lbs[0] != lb.LorebookID {
			t.Errorf("%s: binding %+v, lorebooks %v, %v", id, b, lbs, err)
		}
	}
}

func TestImportDuplicates(t *testing.T) {
	e := setup(t, map[string]any{"fields": []string{"name", "description", "first_mes", "scenario", "character_book"}})
	brakka := map[string]any{"name": "Brakka", "description": "A dwarf smith.", "first_mes": "Hail.", "scenario": "A forge."}
	e.appCard("r1", brakka, nil)
	e.appCard("r2", brakka, nil)
	e.appCard("r3", map[string]any{"name": "Iko", "description": "A courier."}, nil)
	out := e.runImport(nil)
	if out[OutcomeBound] != 1 || out[OutcomeDuplicate] != 1 || out[OutcomeCreated] != 1 {
		t.Fatalf("outcomes = %v", out)
	}
	if bs, err := e.s.Bindings(e.ctx, e.card); err != nil || len(bs) != 1 || bs[0].Status != "in_sync" {
		t.Fatalf("library card's bindings = %+v, %v", bs, err)
	}
	var cards int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM card`).Scan(&cards); err != nil || cards != 2 {
		t.Fatalf("%d cards, %v", cards, err)
	}
	res, err := e.s.ImportCard(e.ctx, e.inst, "r3")
	if err != nil || res.Outcome != OutcomeAlreadyBound {
		t.Fatalf("re-run = %+v, %v", res, err)
	}
}

func TestImportRemoteIDs(t *testing.T) {
	e := setup(t, nil)
	e.appCard("r1", map[string]any{"name": "Iko"}, nil)
	e.appCard("r2", map[string]any{"name": "Tam"}, nil)
	if out := e.runImport([]string{"r2"}); out[OutcomeCreated] != 1 {
		t.Fatalf("outcomes = %v", out)
	}
	if b := e.bindingOf("r2"); b.Status != "in_sync" {
		t.Fatalf("binding = %+v", b)
	}
	_, err := e.s.EnqueueImport(e.ctx, jobs.New(e.pool), e.inst, []string{"r9"})
	if !errors.Is(err, library.ErrInvalid) {
		t.Fatalf("unknown remote ID: %v", err)
	}
}
