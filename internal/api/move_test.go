package api

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
)

// hidePayloads returns the payloads of the queued hide jobs.
func (e *testEnv) hidePayloads() []bulk.HidePayload {
	e.t.Helper()
	list, err := e.s.Queue.List(context.Background(), jobs.ListOptions{Kind: bulk.HideKind})
	if err != nil {
		e.t.Fatal(err)
	}
	out := make([]bulk.HidePayload, len(list))
	for i, j := range list {
		if err := json.Unmarshal(j.Payload, &out[i]); err != nil {
			e.t.Fatal(err)
		}
	}
	return out
}

func TestMoveCardsWithVariantsAndPreviewChangesNothing(t *testing.T) {
	e := newEnv(t)
	archive := e.createLibrary("Archive")
	parent := e.createCard(`{"name": "Brakka"}`)
	var variant entityOut
	e.call("POST", fmt.Sprintf("/cards/%d/variants", parent.ID), map[string]any{}, 201, &variant)
	var coll struct{ ID int64 }
	e.call("POST", "/collections", map[string]any{"name": "Favorites"}, 201, &coll)
	e.call("PUT", fmt.Sprintf("/collections/%d/cards/%d", coll.ID, parent.ID), nil, 204, nil)
	body := map[string]any{"ids": []int64{parent.ID}, "library": archive.ID}

	var preview, moved library.MoveResult
	e.call("POST", "/cards/move/preview", body, 200, &preview)
	var got entityOut
	e.call("GET", fmt.Sprintf("/cards/%d", variant.ID), nil, 200, &got)
	if got.LibraryID != library.DefaultLibrary {
		t.Fatalf("preview moved the variant to library %d", got.LibraryID)
	}
	e.call("POST", "/cards/move", body, 200, &moved)
	want := library.MoveResult{Moved: []int64{parent.ID, variant.ID}, CollectionMembershipsDropped: 1,
		VariantLinksCleared: []int64{}, FromLibraries: []int64{library.DefaultLibrary}, BoundInstances: []int64{}}
	for name, r := range map[string]library.MoveResult{"preview": preview, "move": moved} {
		if fmt.Sprint(r) != fmt.Sprint(want) {
			t.Errorf("%s = %+v, want %+v", name, r, want)
		}
	}
	e.call("GET", fmt.Sprintf("/cards/%d", variant.ID), nil, 200, &got)
	if got.LibraryID != archive.ID || got.VariantOf == nil || got.VariantOf.ID != parent.ID {
		t.Fatalf("variant after move = %+v", got)
	}
	if p := e.hidePayloads(); len(p) != 0 {
		t.Fatalf("hide jobs queued with duplicates off: %+v", p)
	}

	e.call("POST", "/cards/move", map[string]any{"ids": []int64{999999}, "library": archive.ID}, 404, nil)
	e.call("POST", "/cards/move/preview", map[string]any{"ids": []int64{parent.ID}, "library": 999999}, 404, nil)
	e.call("POST", "/cards/move", map[string]any{"ids": []int64{}, "library": archive.ID}, 400, nil)
}

func TestMoveCardQueuesHideInLibrariesThatHideDuplicates(t *testing.T) {
	e := newEnv(t)
	archive := e.createLibrary("Archive")
	for _, lib := range []int64{library.DefaultLibrary, archive.ID} {
		e.call("PATCH", fmt.Sprintf("/libraries/%d/settings", lib), map[string]any{"duplicates": "exact"}, 200, nil)
	}
	c := e.createCard(`{"name": "Brakka"}`)
	e.call("POST", "/cards/move", map[string]any{"ids": []int64{c.ID}, "library": archive.ID}, 200, nil)
	var libs []int64
	for _, p := range e.hidePayloads() {
		libs = append(libs, p.Library)
	}
	slices.Sort(libs)
	if !slices.Equal(libs, []int64{library.DefaultLibrary, archive.ID}) {
		t.Fatalf("hide jobs queued for libraries %v", libs)
	}

	// Lorebooks aren't hidden, so moving one queues nothing.
	var lb entityOut
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Forge"}`)}, 201, &lb)
	var moved library.MoveResult
	e.call("POST", "/lorebooks/move", map[string]any{"ids": []int64{lb.ID}, "library": archive.ID}, 200, &moved)
	if !slices.Equal(moved.Moved, []int64{lb.ID}) {
		t.Fatalf("lorebook move = %+v", moved)
	}
	e.call("GET", fmt.Sprintf("/lorebooks/%d", lb.ID), nil, 200, &lb)
	if lb.LibraryID != archive.ID {
		t.Fatalf("lorebook in library %d", lb.LibraryID)
	}
	if p := e.hidePayloads(); len(p) != 2 {
		t.Fatalf("hide jobs after a lorebook move = %+v", p)
	}
}
