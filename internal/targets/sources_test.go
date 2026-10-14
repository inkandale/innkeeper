package targets

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/db"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

func TestPullOnlyBindingsMayShareAnItem(t *testing.T) {
	e := setup(t, nil)
	insert := func(card int64, push bool) error {
		_, err := e.pool.Exec(e.ctx, `INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data,
			accepted_hash, push) VALUES ($1, $2, 'shared', 'unknown', '{}', '', $3)`, e.inst, card, push)
		return err
	}
	if err := insert(e.card, false); err != nil {
		t.Fatal(err)
	}
	if err := insert(e.newCard("Tova"), false); err != nil {
		t.Fatalf("second pull-only binding on one item: %v", err)
	}
	if err := insert(e.newCard("Pell"), true); err != nil {
		t.Fatalf("first push binding on the item: %v", err)
	}
	if err := insert(e.newCard("Iko"), true); !db.IsCode(err, db.UniqueViolation) {
		t.Fatalf("second push binding on one item = %v, want a unique violation", err)
	}
}

func TestAPullOnlyBindingIsNeverAhead(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	if _, err := e.pool.Exec(e.ctx, `UPDATE binding SET push = false WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A dwarf smith, retired.") })
	if got := e.status(e.card); got != "in_sync" {
		t.Fatalf("status = %s, want in_sync", got)
	}
}

// addUpstream creates an instance of the pull-only fake. It keeps each site
// page as <updir>/fake:<ref>.json, and config adds to its settings.
func (e *env) addUpstream(config map[string]any) {
	e.t.Helper()
	e.updir = e.t.TempDir()
	cfg := map[string]any{"dir": e.updir, "fields": []string{"name", "description", "first_mes"}, "lorebooks": "standalone"}
	maps.Copy(cfg, config)
	id, err := e.reg.CreateInstance(e.ctx, plugins.NewInstance{Plugin: "upstream", Name: "Sites", Config: cfg})
	if err != nil {
		e.t.Fatal(err)
	}
	e.up = id
}

// publish puts a card's page on the fake site as its author last saved it.
// avatar is an image file, or empty.
func (e *env) publish(ref string, data map[string]any, avatar string) {
	e.t.Helper()
	item := map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data}
	if avatar != "" {
		item["avatar"] = avatar
	}
	e.writeSite(ref, item)
}

func (e *env) writeSite(ref string, item map[string]any) {
	e.t.Helper()
	raw, _ := json.Marshal(item)
	if err := os.WriteFile(filepath.Join(e.updir, "fake:"+ref+".json"), raw, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// imported creates a card as an import plugin would, with the fake source ref.
func (e *env) imported(ref string, data map[string]any) int64 {
	e.t.Helper()
	raw, _ := json.Marshal(data)
	c, _, err := e.lib.Cards.CreateFromName(e.ctx, data["name"].(string), "upload", library.Edit{Data: raw, Source: "plugin:upstream"})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.lib.Cards.AddSource(e.ctx, c.ID, library.Source{Kind: "fake", Ref: ref}); err != nil {
		e.t.Fatal(err)
	}
	return c.ID
}

func TestPushRefusesAPullOnlyTarget(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	if _, err := e.s.Push(e.ctx, library.Card, e.up, e.card, false); !errors.Is(err, ErrCannot) {
		t.Fatalf("push = %v, want ErrCannot", err)
	}
}

func TestAPullOnlyTargetCanBeRead(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	if items, err := e.s.Items(e.ctx, library.Card, e.up); err != nil || len(items) != 0 {
		t.Fatalf("items = %+v, %v", items, err)
	}
}

// follow checks a card's sources now and returns its one followed binding.
func (e *env) follow(card int64) Binding {
	e.t.Helper()
	list, err := e.s.CheckEntitySources(e.ctx, library.Card, card)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, b := range list {
		if !b.Push {
			return b
		}
	}
	e.t.Fatalf("card %d follows no source: %+v", card, list)
	return Binding{}
}

var vyrCard = map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Who wakes me?"}

func TestFollowingASourceAcceptsTheImportedRevision(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.update(card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "My red dragon.") })
	e.publish("vyr", vyrCard, "")
	b := e.follow(card)
	if b.InstanceID != e.up || b.RemoteID != "fake:vyr" || b.RemoteState != remoteUnchanged || b.Ahead || b.Status != "in_sync" {
		t.Fatalf("binding = %+v", b)
	}
}

func TestFollowingFlagsAnUpdatePublishedSinceTheImport(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	b := e.follow(card)
	if b.RemoteState != remoteChanged || b.Status != "remote_changed" || len(b.Changes) != 1 || b.Changes[0].Field != "first_mes" {
		t.Fatalf("binding = %+v", b)
	}
}

func TestCardsSharingASourceEachFollowIt(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	first, second := e.imported("vyr", vyrCard), e.imported("vyr", vyrCard)
	e.publish("vyr", vyrCard, "")
	a, b := e.follow(first), e.follow(second)
	if a.ID == b.ID || a.RemoteID != "fake:vyr" || b.RemoteID != "fake:vyr" {
		t.Fatalf("bindings = %+v, %+v", a, b)
	}
}

func TestRemovingASourceStopsFollowingIt(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.publish("vyr", vyrCard, "")
	e.follow(card)
	srcs, err := e.lib.Cards.Sources(e.ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.Cards.RemoveSource(e.ctx, card, srcs[0].ID); err != nil {
		t.Fatal(err)
	}
	list, err := e.s.CheckEntitySources(e.ctx, library.Card, card)
	if err != nil || len(list) != 0 {
		t.Fatalf("bindings = %+v, %v", list, err)
	}
}

func TestScanLeavesFollowedSourcesAlone(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.publish("vyr", vyrCard, "")
	b := e.follow(card)
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	if _, err := e.s.Scan(e.ctx, func(ScanProgress) {}); err != nil {
		t.Fatal(err)
	}
	if got, err := e.s.Binding(e.ctx, b.ID); err != nil || got.RemoteState != remoteUnchanged {
		t.Fatalf("after a scan = %+v, %v", got, err)
	}
}
