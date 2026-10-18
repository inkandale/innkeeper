package targets

import (
	"encoding/json"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
)

func TestSyncMatchesOnlyTheAppsLibrary(t *testing.T) {
	e := setup(t, nil)
	archive, err := e.lib.CreateLibrary(e.ctx, "Archive")
	if err != nil {
		t.Fatal(err)
	}
	lia, _ := json.Marshal(map[string]any{"name": "Lia", "description": liaText})
	copyInArchive, _, err := e.lib.Cards.Create(e.ctx, archive.ID, "lia", "upload", library.Edit{Data: lia, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	libraryOf := func(remoteID string) int64 {
		t.Helper()
		c, err := e.lib.Cards.Get(e.ctx, e.bindingOf(remoteID).CardID)
		if err != nil {
			t.Fatal(err)
		}
		return c.LibraryID
	}

	// The app syncs with library 1, so the archive's copy isn't a candidate: the item is new and imports there.
	e.appCard("r7", map[string]any{"name": "Lia", "description": liaText}, nil)
	if it := item(e.autoReconcile(nil), "r7"); it.Verdict != VerdictNew || !it.AutoImported || len(it.Candidates) != 0 {
		t.Fatalf("r7 = %+v", it)
	}
	if got := libraryOf("r7"); got != library.DefaultLibrary {
		t.Fatalf("r7 imported into library %d", got)
	}

	// Syncing with the archive matches its copy, and leaves r7 bound to the library 1 card.
	if err := e.reg.UpdateInstance(e.ctx, e.inst, plugins.InstanceEdit{SyncLibrary: &archive.ID}); err != nil {
		t.Fatal(err)
	}
	e.appCard("r8", map[string]any{"name": "Lia", "description": liaText}, nil)
	snap := e.autoReconcile(nil)
	if it := item(snap, "r8"); it.Verdict != VerdictExact || !it.AutoBound || it.Candidates[0].CardID != copyInArchive.ID {
		t.Fatalf("r8 = %+v", it)
	}
	if it := item(snap, "r7"); it.Verdict != VerdictBound || libraryOf("r7") != library.DefaultLibrary {
		t.Fatalf("r7 after the switch = %+v", it)
	}
}
