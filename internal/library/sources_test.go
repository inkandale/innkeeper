package library

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
)

const chubCard = `{"name": "A", "extensions": {"chub": {"id": 7, "full_path": "u/a"}}}`

// detect records the source url-import finds in chubCard, as the detect-sources job would.
func detect(t *testing.T, lib *Library, s *Entities, id int64) error {
	t.Helper()
	dir := "characters"
	if s.k == Lorebook {
		dir = "lorebooks"
	}
	src := Source{Kind: "chub", Ref: "u/a", ExternalID: "7", URL: "https://chub.ai/" + dir + "/u/a"}
	return lib.Detected(context.Background(), DetectItem{s.k, id}, []Source{src})
}

func TestImportQueuesDetection(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	_, imp := importer(t, lib)
	id := imp("1", chubCard).ID
	if got := mustSources(t, lib.Cards, id); len(got) != 0 {
		t.Fatalf("import recorded %+v; only the job records sources", got)
	}
	q, err := lib.DetectQueue(ctx, DetectItem{}, 10)
	if err != nil || len(q) != 1 || q[0] != (DetectItem{Card, id}) {
		t.Fatalf("queue = %+v, %v", q, err)
	}
	if err := detect(t, lib, lib.Cards, id); err != nil {
		t.Fatal(err)
	}
	if q, _ := lib.DetectQueue(ctx, DetectItem{}, 10); len(q) != 0 {
		t.Fatalf("queue after Detected = %+v", q)
	}
	if got := mustSources(t, lib.Cards, id); len(got) != 1 || got[0].Ref != "u/a" {
		t.Fatalf("sources = %+v", got)
	}
}

func TestDetectQueuePages(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst, _ := importer(t, lib)
	var want []DetectItem
	for i, k := range []*Kind{Card, Lorebook, Card} {
		data := `{"name": "A", "entries": []}`
		if k == Card {
			data = `{"spec": "chara_card_v3", "data": {"name": "A"}}`
		}
		got, err := lib.ImportItem(ctx, PluginItem{Kind: k, InstanceID: inst, Provenance: fmt.Sprintf("p:%d", i), Source: "plugin:p", Data: []byte(data)})
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, DetectItem{k, got.ID})
	}
	var got []DetectItem
	for after := (DetectItem{}); ; {
		page, err := lib.DetectQueue(ctx, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		got, after = append(got, page...), page[len(page)-1]
	}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[2] || got[2] != want[1] {
		t.Fatalf("queue = %+v, want cards then lorebooks: %+v", got, want)
	}
}

func newLorebook(t *testing.T, lib *Library, slug string) int64 {
	t.Helper()
	l, _, err := lib.Lorebooks.Create(context.Background(), DefaultLibrary, slug, "upload", edit(`{"name": "L"}`))
	if err != nil {
		t.Fatal(err)
	}
	return l.ID
}

func TestSourcesLifecycle(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	id, _ := newCard(t, lib, "a", `{"name": "A"}`)
	other, _ := newCard(t, lib, "b", `{"name": "B"}`)
	s := lib.Cards

	for range 2 {
		if err := detect(t, lib, s, id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Sources(ctx, id)
	if err != nil || len(got) != 1 || got[0].LinkedBy != "auto" || got[0].ExternalID != "7" || got[0].Ref != "u/a" {
		t.Fatalf("after detect twice: %+v, %v", got, err)
	}

	// Remove the auto row: detection must not bring it back.
	if err := s.RemoveSource(ctx, id, got[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := detect(t, lib, s, id); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Sources(ctx, id); len(got) != 0 {
		t.Fatalf("removed source came back: %+v", got)
	}
	if ids, _ := s.BySource(ctx, "chub", "u/a"); len(ids) != 0 {
		t.Fatalf("BySource saw a removed row: %v", ids)
	}
	if err := s.RemoveSource(ctx, id, sourceID(t, s, id)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove twice: %v", err)
	}

	// Adding the removed ref restores it as owner.
	added, err := s.AddSource(ctx, id, Source{Kind: "chub", Ref: "u/a"})
	if err != nil || added.LinkedBy != "owner" {
		t.Fatalf("restore: %+v, %v", added, err)
	}
	if err := detect(t, lib, s, id); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Sources(ctx, id); len(got) != 1 || got[0].LinkedBy != "owner" || got[0].ID != added.ID {
		t.Fatalf("after restore and detect: %+v", got)
	}

	// Two cards share a source; an owner add is listed.
	if _, err := s.AddSource(ctx, other, Source{Kind: "chub", Ref: "u/a"}); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.BySource(ctx, "chub", "u/a"); !slices.Equal(ids, []int64{id, other}) {
		t.Fatalf("BySource = %v", ids)
	}
	if err := s.RemoveSource(ctx, other, added.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other card's source: %v", err)
	}

	// An empty external_id and url keep what is stored.
	kept, err := s.AddSource(ctx, id, Source{Kind: "site", Ref: "x", ExternalID: "9", URL: "https://site.example/x"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.AddSource(ctx, id, Source{Kind: "site", Ref: "x"})
	if err != nil || again.ID != kept.ID || again.ExternalID != "9" || again.URL != "https://site.example/x" {
		t.Fatalf("re-add with empty fields: %+v, %v", again, err)
	}
	// Detection never refreshes an owner row.
	if _, err := s.AddSource(ctx, id, Source{Kind: "chub", Ref: "u/a", ExternalID: "1", URL: "https://example.org/mine"}); err != nil {
		t.Fatal(err)
	}
	if err := detect(t, lib, s, id); err != nil {
		t.Fatal(err)
	}
	for _, src := range mustSources(t, s, id) {
		if src.Ref == "u/a" && (src.LinkedBy != "owner" || src.ExternalID != "1" || src.URL != "https://example.org/mine") {
			t.Fatalf("detect changed an owner row: %+v", src)
		}
	}

	for _, bad := range []Source{{Ref: "x"}, {Kind: "chub"}, {Kind: "Chub", Ref: "x"}} {
		if _, err := s.AddSource(ctx, id, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: got %v, want ErrInvalid", bad, err)
		}
	}
	if _, err := s.AddSource(ctx, 999, Source{Kind: "chub", Ref: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func mustSources(t *testing.T, s *Entities, id int64) []Source {
	t.Helper()
	got, err := s.Sources(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func sourceID(t *testing.T, s *Entities, id int64) int64 {
	t.Helper()
	var sid int64
	if err := s.pool.QueryRow(context.Background(), `SELECT id FROM `+s.k.SourceTable+` WHERE `+s.k.FK+` = $1`, id).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	return sid
}

func TestLorebookSources(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, b := newLorebook(t, lib, "a"), newLorebook(t, lib, "b")
	s := lib.Lorebooks
	if err := detect(t, lib, s, a); err != nil {
		t.Fatal(err)
	}
	got, err := s.Sources(ctx, a)
	if err != nil || len(got) != 1 || got[0].URL != "https://chub.ai/lorebooks/u/a" {
		t.Fatalf("detect: %+v, %v", got, err)
	}
	if err := s.RemoveSource(ctx, b, got[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other lorebook's source: %v", err)
	}
	if err := s.RemoveSource(ctx, a, got[0].ID); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.BySource(ctx, "chub", "u/a"); len(ids) != 0 {
		t.Fatalf("BySource saw a removed row: %v", ids)
	}
	if err := detect(t, lib, s, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Sources(ctx, a); len(got) != 0 {
		t.Fatalf("removed source came back: %+v", got)
	}
	// A card's source of the same ref is separate.
	if ids, _ := lib.Cards.BySource(ctx, "chub", "u/a"); len(ids) != 0 {
		t.Fatalf("cards saw a lorebook source: %v", ids)
	}
}
