package targets

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/merge"
	"github.com/inkandale/innkeeper/internal/plugins"
)

// archive imports vyr from an import-only instance, then edits its
// description as the owner, and returns the instance and the card.
func (e *env) archive(description string) (int64, int64) {
	e.t.Helper()
	inst, err := e.reg.CreateInstance(e.ctx, plugins.NewInstance{Plugin: "archive", Name: new("Archive")})
	if err != nil {
		e.t.Fatal(err)
	}
	card := e.importVersion(inst, vyrCard).ID
	e.update(card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", description) })
	return inst, card
}

func (e *env) importVersion(inst int64, data map[string]any) library.Imported {
	e.t.Helper()
	raw, _ := json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data})
	got, err := e.lib.ImportItem(e.ctx, library.PluginItem{Kind: library.Card, InstanceID: inst, Provenance: "archive:vyr",
		Source: "plugin:archive", Data: raw})
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

func (e *env) importBinding(card int64) Binding {
	e.t.Helper()
	var id int64
	if err := e.pool.QueryRow(e.ctx, `SELECT id FROM binding WHERE card_id = $1 AND imported`, card).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	b, err := e.s.Binding(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *env) field(card int64, key string) any {
	e.t.Helper()
	var data map[string]any
	if err := json.Unmarshal(e.head(card).Data, &data); err != nil {
		e.t.Fatal(err)
	}
	return data[key]
}

func TestImportUpdateIsReviewedInTheChangesQueue(t *testing.T) {
	e := setup(t, nil)
	inst, card := e.archive("My red dragon.")
	update := map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}
	if got := e.importVersion(inst, update); got.Outcome != library.Pending {
		t.Fatalf("import = %+v, want a pending update", got)
	}

	q, err := e.s.Changes(e.ctx, ChangesFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var entry *AppChange
	for _, r := range q.Rows {
		for i, c := range r.Apps {
			if r.CardID == card && c.InstanceID == inst {
				entry = &r.Apps[i]
			}
		}
	}
	if entry == nil || entry.State != ChangeUpdate || entry.Push || entry.RemoteID != "archive:vyr" {
		t.Fatalf("queue = %+v", q.Rows)
	}

	// The last import is the ancestor, so the owner's edit and the update merge.
	c, err := e.s.CompareItem(e.ctx, library.Card, inst, entry.RemoteID, card)
	if err != nil {
		t.Fatal(err)
	}
	if c.Base != "accepted" || c.AppHash != entry.AppHash || hasConflict(c) {
		t.Fatalf("comparison = %+v", c)
	}
	if _, err := e.s.Resolve(e.ctx, library.Card, Resolution{InstanceID: inst, RemoteID: entry.RemoteID, AppHash: c.AppHash,
		BaseRevisionID: c.HeadRevisionID, AcceptedHash: c.AcceptedHash, Action: "bind", CardID: card, Default: merge.ChooseApp}); err != nil {
		t.Fatal(err)
	}
	if e.field(card, "description") != "My red dragon." || e.field(card, "first_mes") != "Begone." {
		t.Fatalf("head = %s", e.head(card).Data)
	}
	if b := e.importBinding(card); b.RemoteState != remoteUnchanged {
		t.Fatalf("binding after resolve = %+v", b)
	}

	// The merged head isn't the last import, so the next version waits too.
	next := map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone, thief."}
	if got := e.importVersion(inst, next); got.Outcome != library.Pending {
		t.Fatalf("next import = %+v, want a pending update", got)
	}
}

func TestAutosyncAppliesACleanImportUpdate(t *testing.T) {
	e := setup(t, nil)
	inst, card := e.archive("My red dragon.")
	b := e.importBinding(card)
	if _, err := e.s.SetAutosync(e.ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}
	got := e.importVersion(inst, map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."})
	if len(got.Autosync) != 1 || got.Autosync[0] != b.ID {
		t.Fatalf("import = %+v", got)
	}
	applied, err := e.s.ApplyUpdate(e.ctx, b.ID)
	if err != nil || !applied {
		t.Fatalf("apply = %v, %v", applied, err)
	}
	if e.field(card, "description") != "My red dragon." || e.field(card, "first_mes") != "Begone." {
		t.Fatalf("head = %s", e.head(card).Data)
	}
}

func TestAutosyncLeavesAConflictingImportUpdate(t *testing.T) {
	e := setup(t, nil)
	inst, card := e.archive("A blue dragon.")
	b := e.importBinding(card)
	if _, err := e.s.SetAutosync(e.ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}
	e.importVersion(inst, map[string]any{"name": "Vyr", "description": "A gold dragon.", "first_mes": "Who wakes me?"})
	before := e.revisions(card)
	// A source check is how autosync catches up with an update recorded before it was on.
	p, err := e.s.CheckSources(e.ctx, library.DefaultLibrary, func(ScanProgress) {})
	if err != nil || p.Applied != 0 || p.Failed != 0 || e.revisions(card) != before {
		t.Fatalf("progress = %+v, %v", p, err)
	}
	if got := e.importBinding(card); got.RemoteState != remoteChanged {
		t.Fatalf("binding = %+v", got)
	}
}

func TestSourceCheckAppliesAWaitingImportUpdate(t *testing.T) {
	e := setup(t, nil)
	inst, card := e.archive("My red dragon.")
	e.importVersion(inst, map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."})
	b := e.importBinding(card)
	if _, err := e.s.SetAutosync(e.ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}
	p, err := e.s.CheckSources(e.ctx, library.DefaultLibrary, func(ScanProgress) {})
	if err != nil || p.Applied != 1 || p.Failed != 0 {
		t.Fatalf("progress = %+v, %v", p, err)
	}
	if e.field(card, "first_mes") != "Begone." {
		t.Fatalf("head = %s", e.head(card).Data)
	}
}

func TestImportBindingSitsBesideAFollowedSource(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	got := e.importVersion(e.up, vyrCard)
	if _, err := e.lib.Cards.AddSource(e.ctx, got.ID, library.Source{Kind: "fake", Ref: "vyr"}); err != nil {
		t.Fatal(err)
	}
	e.publish("vyr", vyrCard, "")
	if b := e.follow(got.ID); b.RemoteID != "fake:vyr" {
		t.Fatalf("followed binding = %+v", b)
	}
	// The source check neither reads nor drops the import binding.
	if _, err := e.s.CheckSources(e.ctx, library.DefaultLibrary, func(ScanProgress) {}); err != nil {
		t.Fatal(err)
	}
	if b := e.importBinding(got.ID); b.RemoteID != "archive:vyr" || b.RemoteState != remoteUnchanged {
		t.Fatalf("import binding = %+v", b)
	}
}

// tmpAvatar copies an image into the blob store's tmp folder, as a plugin sends one.
func (e *env) tmpAvatar(png string) string {
	e.t.Helper()
	raw, err := os.ReadFile(png)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.MkdirAll(e.blobTmp, 0o755); err != nil {
		e.t.Fatal(err)
	}
	f, err := os.CreateTemp(e.blobTmp, "avatar-*.png")
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(raw); err != nil {
		e.t.Fatal(err)
	}
	return f.Name()
}

func TestImportAfterKeepingTheLibraryAvatarRevisesTheCard(t *testing.T) {
	e := setup(t, nil)
	inst, err := e.reg.CreateInstance(e.ctx, plugins.NewInstance{Plugin: "archive", Name: new("Archive")})
	if err != nil {
		t.Fatal(err)
	}
	site := patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) })
	mine := patternPNG(t, func(_, y int) uint8 { return uint8(y / 8 % 2 * 255) })
	site2 := patternPNG(t, func(x, y int) uint8 { return uint8((x + y) / 8 % 2 * 255) })
	send := func(data map[string]any, avatar string) library.Imported {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data})
		got, err := e.lib.ImportItem(e.ctx, library.PluginItem{Kind: library.Card, InstanceID: inst, Provenance: "archive:vyr",
			Source: "plugin:archive", Data: raw, Avatar: e.tmpAvatar(avatar)})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	card := send(vyrCard, site).ID
	// The owner swaps the avatar, and the site's new avatar and text wait for review.
	ownBlob, err := e.lib.IngestBlob(e.ctx, e.tmpAvatar(mine))
	if err != nil {
		t.Fatal(err)
	}
	e.update(card, func(ed *library.Edit) { ed.AvatarBlob = ownBlob.SHA256 })
	v2 := map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}
	if got := send(v2, site2); got.Outcome != library.Pending {
		t.Fatalf("v2 = %+v", got)
	}
	// Review takes the site's text and keeps the owner's avatar.
	b := e.importBinding(card)
	c, err := e.s.CompareItem(e.ctx, library.Card, inst, b.RemoteID, card)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Resolve(e.ctx, library.Card, Resolution{InstanceID: inst, RemoteID: b.RemoteID, AppHash: c.AppHash,
		BaseRevisionID: c.HeadRevisionID, AcceptedHash: c.AcceptedHash, Action: "bind", CardID: card, Default: merge.ChooseApp}); err != nil {
		t.Fatal(err)
	}
	// The text is as last imported, so the next text change is taken, with the owner's avatar.
	v3 := map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone, thief."}
	if got := send(v3, site2); got.Outcome != library.Revised {
		t.Fatalf("v3 = %+v, want a revision", got)
	}
	if h := e.head(card); h.AvatarBlob == nil || *h.AvatarBlob != ownBlob.SHA256 || e.field(card, "first_mes") != "Begone, thief." {
		t.Fatalf("head = %s with avatar %v", h.Data, h.AvatarBlob)
	}
	if got := send(v3, site2); got.Outcome != library.Unchanged {
		t.Fatalf("v3 again = %+v", got)
	}
}

func TestBindBesideAnImportBinding(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	got := e.importVersion(e.up, vyrCard)
	e.publish("vyr", vyrCard, "")
	b, err := e.s.Bind(e.ctx, library.Card, e.up, got.ID, "fake:vyr")
	if err != nil || b.RemoteID != "fake:vyr" || b.Imported {
		t.Fatalf("bind = %+v, %v", b, err)
	}
}
