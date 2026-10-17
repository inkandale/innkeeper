package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/importer"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/targets"
)

// createLibrary adds a library through the API and returns it.
func (e *testEnv) createLibrary(name string) library.LibrarySummary {
	e.t.Helper()
	var l library.LibrarySummary
	e.call("POST", "/libraries", map[string]any{"name": name}, 201, &l)
	return l
}

func TestLibraryRoutes(t *testing.T) {
	e := newEnv(t)
	e.createCard(`{"name": "Brakka"}`)
	archive := e.createLibrary("Archive")
	e.call("POST", "/libraries", map[string]any{"name": "Archive"}, 409, nil)
	e.call("POST", "/libraries", map[string]any{"name": ""}, 422, nil)

	var list []library.LibrarySummary
	e.call("GET", "/libraries", nil, 200, &list)
	if len(list) != 2 || list[0].ID != library.DefaultLibrary || list[0].Cards != 1 || list[1].ID != archive.ID || list[1].Name != "Archive" {
		t.Fatalf("libraries = %+v", list)
	}

	var renamed library.LibrarySummary
	e.call("PATCH", fmt.Sprintf("/libraries/%d", archive.ID), map[string]any{"name": "Chub archive"}, 200, &renamed)
	if renamed.Name != "Chub archive" {
		t.Fatalf("renamed = %+v", renamed)
	}
	e.call("PATCH", fmt.Sprintf("/libraries/%d", archive.ID), map[string]any{"name": "Library"}, 409, nil)
	e.call("PATCH", "/libraries/999", map[string]any{"name": "Gone"}, 404, nil)
}

func TestDeleteLibraryRoute(t *testing.T) {
	e := newEnv(t)
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{bulk.DeleteLibraryKind: bulk.DeleteLibrary(e.s.Lib)}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	archive := e.createLibrary("Archive")
	var c entityResult
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Gone"}, "library": archive.ID}, 201, &c)
	path := fmt.Sprintf("/libraries/%d", archive.ID)

	e.call("DELETE", path, map[string]any{"confirm": "archive"}, 422, nil)
	e.call("DELETE", "/libraries/1", map[string]any{"confirm": "Library"}, 409, nil)
	e.call("DELETE", "/libraries/999", map[string]any{"confirm": "x"}, 404, nil)
	var j jobOut
	e.call("DELETE", path, map[string]any{"confirm": "Archive"}, 202, &j)
	if last, r := e.watchJob(j.ID); last.Status != "succeeded" {
		t.Fatalf("delete = %+v\n%s", last, r.Body)
	}
	e.call("GET", fmt.Sprintf("/cards/%d", c.ID), nil, 404, nil)
	var list []library.LibrarySummary
	e.call("GET", "/libraries", nil, 200, &list)
	if len(list) != 1 {
		t.Fatalf("libraries = %+v", list)
	}
}

func TestLibraryParameter(t *testing.T) {
	e := newEnv(t)
	archive := e.createLibrary("Archive")
	here := e.createCard(`{"name": "Brakka", "tags": ["dwarf"]}`)
	var there entityResult
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Brakka", "tags": []string{"elf"}}, "library": archive.ID}, 201, &there)
	if there.LibraryID != archive.ID {
		t.Fatalf("created in library %d", there.LibraryID)
	}
	var book entityResult
	e.call("POST", "/lorebooks", map[string]any{"data": map[string]any{"name": "Lore", "entries": []any{}}, "library": archive.ID}, 201, &book)
	if book.LibraryID != archive.ID {
		t.Fatalf("lorebook in library %d", book.LibraryID)
	}

	var res searchResults
	e.call("POST", "/cards/search", map[string]any{"library": archive.ID}, 200, &res)
	if len(res.Hits) != 1 || res.Hits[0].ID != there.ID {
		t.Fatalf("archive search = %+v", res.Hits)
	}
	e.call("POST", "/cards/search", map[string]any{}, 200, &res)
	if len(res.Hits) != 1 || res.Hits[0].ID != here.ID {
		t.Fatalf("default search = %+v", res.Hits)
	}
	var tags []library.TagCount
	e.call("GET", fmt.Sprintf("/tags?library=%d", archive.ID), nil, 200, &tags)
	if len(tags) != 1 || tags[0].Name != "elf" {
		t.Fatalf("tags = %+v", tags)
	}

	var col library.Collection
	e.call("POST", "/collections", map[string]any{"name": "Crew", "library": archive.ID}, 201, &col)
	if col.LibraryID != archive.ID {
		t.Fatalf("collection = %+v", col)
	}
	var cols []library.Collection
	e.call("GET", fmt.Sprintf("/collections?library=%d", archive.ID), nil, 200, &cols)
	if len(cols) != 1 || cols[0].ID != col.ID {
		t.Fatalf("collections = %+v", cols)
	}
	e.call("GET", "/collections", nil, 200, &cols)
	if len(cols) != 0 {
		t.Fatalf("default collections = %+v", cols)
	}
	e.call("PUT", fmt.Sprintf("/collections/%d/cards/%d", col.ID, here.ID), nil, 400, nil)
	e.call("PUT", fmt.Sprintf("/cards/%d/variant_of", there.ID), map[string]any{"id": here.ID}, 400, nil)

	var fork entityResult
	e.call("POST", fmt.Sprintf("/cards/%d/variants", here.ID), map[string]any{"library": archive.ID}, 201, &fork)
	if fork.LibraryID != archive.ID {
		t.Fatalf("fork in library %d", fork.LibraryID)
	}
	var srcs []library.Source
	e.call("GET", fmt.Sprintf("/cards/%d/sources", fork.ID), nil, 200, &srcs)
	if len(srcs) != 1 || srcs[0].Kind != library.ForkSourceKind || srcs[0].Ref != strconv.FormatInt(here.ID, 10) {
		t.Fatalf("fork sources = %+v", srcs)
	}

	var j jobOut
	e.call("POST", "/imports", map[string]any{"instance_id": e.importID, "library": archive.ID}, 202, &j)
	var p importer.Payload
	if err := json.Unmarshal(j.Payload, &p); err != nil || p.Library != archive.ID {
		t.Fatalf("import payload = %s, %v", j.Payload, err)
	}
	e.call("POST", fmt.Sprintf("/source-checks?library=%d", archive.ID), nil, 202, &j)
	var sp targets.SourceCheckPayload
	if err := json.Unmarshal(j.Payload, &sp); err != nil || sp.Library != archive.ID {
		t.Fatalf("source check payload = %s, %v", j.Payload, err)
	}

	// A library that doesn't exist is a 422 everywhere.
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/cards/search", map[string]any{"library": 999}},
		{"POST", "/lorebooks/search", map[string]any{"library": 999}},
		{"POST", "/cards", map[string]any{"data": map[string]any{"name": "X"}, "library": 999}},
		{"POST", "/lorebooks", map[string]any{"data": map[string]any{"name": "X", "entries": []any{}}, "library": 999}},
		{"POST", fmt.Sprintf("/cards/%d/variants", here.ID), map[string]any{"library": 999}},
		{"POST", "/imports", map[string]any{"instance_id": e.importID, "library": 999}},
		{"POST", "/collections", map[string]any{"name": "X", "library": 999}},
		{"GET", "/collections?library=999", nil},
		{"GET", "/tags?library=999", nil},
		{"POST", "/source-checks?library=999", nil},
		{"POST", "/bulk", map[string]any{"action": "tag", "tags": []string{"x"}, "query": map[string]any{"text": "Brakka", "library": 999}}},
	} {
		e.call(req.method, req.path, req.body, 422, nil)
	}
}

func TestInOtherLibrariesRoute(t *testing.T) {
	e := newEnv(t)
	archive := e.createLibrary("Archive")
	data := map[string]any{"name": "Brakka", "description": "A dwarf smith who forges runes into every blade she makes."}
	here := e.createCard(`{"name": "Brakka", "description": "A dwarf smith who forges runes into every blade she makes."}`)
	var there entityResult
	e.call("POST", "/cards", map[string]any{"data": data, "library": archive.ID}, 201, &there)

	var got []library.HashMatch
	e.call("GET", fmt.Sprintf("/cards/%d/in-other-libraries", here.ID), nil, 200, &got)
	if len(got) != 1 || got[0] != (library.HashMatch{ID: there.ID, LibraryID: archive.ID, Reason: "content"}) {
		t.Fatalf("in other libraries = %+v", got)
	}
	e.call("GET", "/cards/999/in-other-libraries", nil, 404, nil)
	e.call("GET", "/lorebooks/999/in-other-libraries", nil, 404, nil)
}
