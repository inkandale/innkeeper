package targets

import (
	"bytes"
	"encoding/json"
	"errors"
	"image/png"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/db"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/merge"
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

func TestFollowingASourceAcceptsTheFirstPluginRevision(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	draft := map[string]any{"name": "Vyr", "description": "A dragon, someday.", "first_mes": "..."}
	raw, _ := json.Marshal(draft)
	c, _, err := e.lib.Cards.CreateFromName(e.ctx, "Vyr", "upload", library.Edit{Data: raw, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.lib.Cards.AddSource(e.ctx, c.ID, library.Source{Kind: "fake", Ref: "vyr"}); err != nil {
		t.Fatal(err)
	}
	e.update(c.ID, func(ed *library.Edit) {
		ed.Data, _ = json.Marshal(vyrCard)
		ed.Source = "plugin:upstream"
	})
	e.publish("vyr", vyrCard, "")
	if b := e.follow(c.ID); b.RemoteState != remoteUnchanged {
		t.Fatalf("binding = %+v, want the plugin revision as its baseline", b)
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

func TestANewAvatarOnTheSiteIsAnUpdate(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	stripes := patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) })
	rings := patternPNG(t, func(x, y int) uint8 { return uint8((x-32)*(x-32)+(y-32)*(y-32)) / 128 * 255 })
	e.publish("vyr", vyrCard, stripes)
	if b := e.follow(card); b.RemoteState != remoteUnchanged {
		t.Fatalf("first check = %+v, want the avatar accepted silently", b)
	}
	e.publish("vyr", vyrCard, rings)
	if b := e.follow(card); b.RemoteState != remoteChanged {
		t.Fatalf("after a new avatar = %+v", b)
	}
}

func TestARecodedAvatarIsNotAnUpdate(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	stripes := patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) })
	e.publish("vyr", vyrCard, stripes)
	e.follow(card)
	e.publish("vyr", vyrCard, recoded(t, stripes))
	if b := e.follow(card); b.RemoteState != remoteUnchanged {
		t.Fatalf("after the same picture, re-encoded = %+v", b)
	}
}

// recoded writes the PNG at path again without compression: the same pixels
// in different bytes.
func recoded(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "recoded.png")
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFailedChecksOfASourceAreCounted(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(map[string]any{"fail_get": "retryable"})
	card := e.imported("vyr", vyrCard)
	var b Binding
	for range 3 {
		b = e.follow(card)
	}
	if b.CheckFailures != 3 || !strings.Contains(b.CheckError, "app unavailable") {
		t.Fatalf("binding = %+v", b)
	}
}

func TestASourceGoneFromTheSiteIsMissing(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	if b := e.follow(card); b.RemoteState != remoteMissing || b.CheckFailures != 0 {
		t.Fatalf("binding = %+v", b)
	}
}

func TestAPausedSourceIsNotChecked(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.publish("vyr", vyrCard, "")
	b := e.follow(card)
	if _, err := e.s.SetPaused(e.ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	if got := e.follow(card); got.RemoteState != remoteUnchanged || !got.Paused {
		t.Fatalf("paused = %+v", got)
	}
	if _, err := e.s.SetPaused(e.ctx, b.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := e.follow(card); got.RemoteState != remoteChanged {
		t.Fatalf("checked again = %+v", got)
	}
}

// followedStates maps each card or lorebook in the queue to its upstream entry's state.
func (e *env) followedStates() (cards, lorebooks map[int64]AppChange) {
	e.t.Helper()
	ch, err := e.s.Changes(e.ctx, ChangesFilter{})
	if err != nil {
		e.t.Fatal(err)
	}
	cards, lorebooks = map[int64]AppChange{}, map[int64]AppChange{}
	for _, r := range ch.Rows {
		for _, a := range r.Apps {
			if a.InstanceID != e.up {
				continue
			}
			if r.LorebookID != 0 {
				lorebooks[r.LorebookID] = a
			} else {
				cards[r.CardID] = a
			}
		}
	}
	return cards, lorebooks
}

func TestChangesListSourceUpdates(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	changed, gone, same := e.imported("vyr", vyrCard), e.imported("gone", vyrCard), e.imported("same", vyrCard)
	paused := e.imported("paused", vyrCard)
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	e.publish("paused", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	e.publish("same", vyrCard, "")
	for _, c := range []int64{changed, gone, same} {
		e.follow(c)
	}
	if _, err := e.s.SetPaused(e.ctx, e.follow(paused).ID, true); err != nil {
		t.Fatal(err)
	}
	e.update(same, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "My red dragon.") })
	cards, _ := e.followedStates()
	if cards[changed].State != ChangeUpdate || cards[gone].State != ChangeMissing || len(cards) != 2 {
		t.Fatalf("entries = %+v", cards)
	}
	srcs, _ := e.lib.Cards.Sources(e.ctx, changed)
	if err := e.lib.Cards.RemoveSource(e.ctx, changed, srcs[0].ID); err != nil {
		t.Fatal(err)
	}
	if cards, _ := e.followedStates(); len(cards) != 1 {
		t.Fatalf("after removing a source = %+v", cards)
	}
}

func TestChangesShowASourceThatKeepsFailing(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(map[string]any{"fail_get": "retryable"})
	card := e.imported("vyr", vyrCard)
	for range 2 {
		e.follow(card)
	}
	if cards, _ := e.followedStates(); len(cards) != 0 {
		t.Fatalf("after 2 failures = %+v", cards)
	}
	e.follow(card)
	if cards, _ := e.followedStates(); cards[card].State != ChangeError || !strings.Contains(cards[card].Error, "app unavailable") {
		t.Fatalf("after 3 failures = %+v", cards)
	}
}

func TestChangesListALorebookSourceUpdate(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	lb := e.newLorebook("Forge lore")
	if _, err := e.lib.Lorebooks.AddSource(e.ctx, lb, library.Source{Kind: "fake", Ref: "lore"}); err != nil {
		t.Fatal(err)
	}
	e.writeSite("lore", map[string]any{"lorebook": map[string]any{"name": "Forge lore", "description": "Forge lore, revised.",
		"entries": []any{map[string]any{"keys": []any{"forge"}, "content": "The forge burns hot."}}}})
	if _, err := e.s.CheckEntitySources(e.ctx, library.Lorebook, lb); err != nil {
		t.Fatal(err)
	}
	if _, books := e.followedStates(); books[lb].State != ChangeUpdate {
		t.Fatalf("lorebook entries = %+v", books)
	}
}

func TestChangesTellSourcesFromApps(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	e.follow(card)
	e.push(e.card)
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A dwarf smith, retired.") })
	srcs, err := e.lib.Cards.Sources(e.ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := e.s.Changes(e.ctx, ChangesFilter{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]AppChange{}
	for _, r := range ch.Rows {
		for _, a := range r.Apps {
			got[r.CardID] = a
		}
	}
	if a := got[card]; a.Push || a.SourceID != srcs[0].ID {
		t.Fatalf("source entry = %+v, want push false and source %d", a, srcs[0].ID)
	}
	if a := got[e.card]; a.State != ChangeAhead || !a.Push || a.SourceID != 0 {
		t.Fatalf("app entry = %+v", a)
	}
}

func TestReviewingASharedSourceUsesTheCardsOwnBinding(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	first := e.imported("vyr", vyrCard)
	second := e.imported("vyr", map[string]any{"name": "Vyr", "description": "A red dragon, older.", "first_mes": "Who wakes me?"})
	e.publish("vyr", vyrCard, "")
	e.follow(first)
	b := e.follow(second)
	c, err := e.s.CompareItem(e.ctx, library.Card, e.up, b.RemoteID, second)
	if err != nil || c.Base != "accepted" || c.AcceptedHash != b.LastPush.PushedHash {
		t.Fatalf("comparison = %+v, %v; want the second card's accepted copy", c, err)
	}
}

func TestReviewingASourceUpdateKeepsTheOwnersEdits(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.update(card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "My red dragon.") })
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	b := e.follow(card)
	c, err := e.s.CompareItem(e.ctx, library.Card, e.up, b.RemoteID, card)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Resolve(e.ctx, library.Card, Resolution{InstanceID: e.up, RemoteID: b.RemoteID, AppHash: c.AppHash,
		BaseRevisionID: c.HeadRevisionID, AcceptedHash: c.AcceptedHash, Action: "bind", CardID: card,
		Default: merge.ChooseLibrary}); err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	_ = json.Unmarshal(e.head(card).Data, &data)
	if data["description"] != "My red dragon." || data["first_mes"] != "Begone." {
		t.Fatalf("head = %v", data)
	}
	if got, _ := e.s.Binding(e.ctx, b.ID); got.RemoteState != remoteUnchanged {
		t.Fatalf("binding = %+v", got)
	}
}

func TestDismissingASourceUpdateKeepsTheLibrary(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	b := e.follow(card)
	before := e.revisions(card)
	got, err := e.s.Dismiss(e.ctx, b.ID)
	if err != nil || got.RemoteState != remoteUnchanged || e.revisions(card) != before {
		t.Fatalf("dismissed = %+v, %v; revisions %d, want %d", got, err, e.revisions(card), before)
	}
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone, again."}, "")
	if got := e.follow(card); got.RemoteState != remoteChanged {
		t.Fatalf("after another change = %+v", got)
	}
}

func TestDismissingANewAvatarAcceptsIt(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	stripes := patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) })
	rings := patternPNG(t, func(x, y int) uint8 { return uint8((x-32)*(x-32)+(y-32)*(y-32)) / 128 * 255 })
	e.publish("vyr", vyrCard, stripes)
	e.follow(card)
	e.publish("vyr", vyrCard, rings)
	b := e.follow(card)
	if _, err := e.s.Dismiss(e.ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.follow(card); got.RemoteState != remoteUnchanged {
		t.Fatalf("after dismissing the avatar = %+v", got)
	}
}

func TestAnAvatarAddedToASourceWithoutOneIsAnUpdate(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.publish("vyr", vyrCard, "")
	if b := e.follow(card); b.RemoteState != remoteUnchanged {
		t.Fatalf("first check = %+v", b)
	}
	e.publish("vyr", vyrCard, patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) }))
	if b := e.follow(card); b.RemoteState != remoteChanged {
		t.Fatalf("after a first avatar = %+v", b)
	}
}

func TestDismissingAnAvatarRemovalAcceptsIt(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	stripes := patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) })
	e.publish("vyr", vyrCard, stripes)
	e.follow(card)
	e.publish("vyr", vyrCard, "")
	b := e.follow(card)
	if b.RemoteState != remoteChanged {
		t.Fatalf("after removing the avatar = %+v", b)
	}
	if _, err := e.s.Dismiss(e.ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.follow(card); got.RemoteState != remoteUnchanged {
		t.Fatalf("after dismissing the removal = %+v", got)
	}
	e.publish("vyr", vyrCard, stripes)
	if got := e.follow(card); got.RemoteState != remoteChanged {
		t.Fatalf("after the avatar came back = %+v", got)
	}
}
