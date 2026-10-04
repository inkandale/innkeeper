package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type entityResult struct {
	ID         int64          `json:"id"`
	Slug       string         `json:"slug"`
	Provenance string         `json:"provenance"`
	ArchivedAt *string        `json:"archived_at"`
	Tags       []string       `json:"tags"`
	Lorebooks  []int64        `json:"lorebooks"`
	Revision   revisionResult `json:"revision"`
}

type revisionResult struct {
	ID        int64           `json:"id"`
	Data      json.RawMessage `json:"data"`
	Avatar    *string         `json:"avatar"`
	Overrides json.RawMessage `json:"overrides"`
	Message   string          `json:"message"`
	Source    string          `json:"source"`
}

func (e *testEnv) createCard(data string) entityResult {
	e.t.Helper()
	var c entityResult
	e.call("POST", "/cards", map[string]any{"data": json.RawMessage(data)}, 201, &c)
	return c
}

func TestCardLifecycle(t *testing.T) {
	e := newEnv(t)
	c := e.createCard(`{"name": "Brakka Ironfist", "description": "A dwarf."}`)
	if c.Slug != "brakka-ironfist" || c.Provenance != "api" || c.Revision.Source != "ui" {
		t.Fatalf("created %+v", c)
	}
	first := c.Revision.ID
	path := fmt.Sprintf("/cards/%d", c.ID)

	var updated entityResult
	e.call("PUT", path, map[string]any{"base_revision_id": first,
		"data": json.RawMessage(`{"name": "Brakka", "description": "A grumpy dwarf."}`), "message": "grumpier", "source": "mcp"}, 200, &updated)
	if updated.Revision.ID == first || updated.Revision.Source != "mcp" || updated.Revision.Message != "grumpier" {
		t.Fatalf("updated %+v", updated.Revision)
	}
	e.call("PUT", path, map[string]any{"base_revision_id": first, "data": json.RawMessage(`{"name": "Stale"}`)}, 409, nil)

	var history []revisionResult
	e.call("GET", path+"/revisions", nil, 200, &history)
	if len(history) != 2 || history[0].ID != updated.Revision.ID || history[0].Data != nil {
		t.Fatalf("history = %+v", history)
	}
	var old revisionResult
	e.call("GET", fmt.Sprintf("%s/revisions/%d", path, first), nil, 200, &old)
	if !strings.Contains(string(old.Data), "Brakka Ironfist") {
		t.Fatalf("old revision = %s", old.Data)
	}

	var restored entityResult
	e.call("POST", path+"/restore", map[string]any{"revision_id": first, "base_revision_id": updated.Revision.ID}, 200, &restored)
	if !strings.Contains(string(restored.Revision.Data), "Brakka Ironfist") || restored.Revision.Message != fmt.Sprintf("Restore revision %d", first) {
		t.Fatalf("restored %+v", restored.Revision)
	}

	e.call("POST", path+"/restore", map[string]any{"revision_id": first, "base_revision_id": updated.Revision.ID}, 409, nil)

	e.call("PUT", path+"/archived", map[string]bool{"archived": true}, 204, nil)
	var archived entityResult
	e.call("GET", path, nil, 200, &archived)
	if archived.ArchivedAt == nil {
		t.Fatal("card not archived")
	}
}

func TestCardErrors(t *testing.T) {
	e := newEnv(t)
	other := e.createCard(`{"name": "Other"}`)
	e.call("GET", "/cards/999", nil, 404, nil)
	e.call("GET", "/cards/999/similar", nil, 404, nil)
	e.call("POST", "/cards", map[string]any{"data": json.RawMessage(`{"name": "A", "character_book": {}}`)}, 400, nil)
	e.call("POST", "/cards", map[string]any{"slug": "Not A Slug", "data": json.RawMessage(`{"name": "A"}`)}, 422, nil)
	e.call("POST", "/cards", map[string]any{"slug": "other", "data": json.RawMessage(`{"name": "A"}`)}, 409, nil)
	e.call("POST", "/cards", map[string]any{"data": json.RawMessage(`{"name": "A"}`), "avatar": strings.Repeat("a", 64)}, 404, nil)

	c := e.createCard(`{"name": "Card"}`)
	// A revision of another card is never this card's base, and never restorable here.
	e.call("PUT", fmt.Sprintf("/cards/%d", c.ID), map[string]any{"base_revision_id": other.Revision.ID,
		"data": json.RawMessage(`{"name": "X"}`)}, 409, nil)
	e.call("GET", fmt.Sprintf("/cards/%d/revisions/%d", c.ID, other.Revision.ID), nil, 404, nil)
	e.call("POST", fmt.Sprintf("/cards/%d/restore", c.ID), map[string]any{"revision_id": other.Revision.ID,
		"base_revision_id": c.Revision.ID}, 404, nil)
}

func TestUpdateKeepsAvatarAndOverrides(t *testing.T) {
	e := newEnv(t)
	blob, err := e.s.Lib.PutBlob(context.Background(), strings.NewReader("avatar"))
	if err != nil {
		t.Fatal(err)
	}
	var c entityResult
	e.call("POST", "/cards", map[string]any{"data": json.RawMessage(`{"name": "A"}`), "avatar": blob.SHA256}, 201, &c)
	path := fmt.Sprintf("/cards/%d", c.ID)

	e.call("PUT", path, map[string]any{"base_revision_id": c.Revision.ID, "data": json.RawMessage(`{"name": "B"}`),
		"overrides": json.RawMessage(`{"1": {"name": "B for Lumiverse"}}`)}, 200, &c)
	e.call("PUT", path, map[string]any{"base_revision_id": c.Revision.ID, "data": json.RawMessage(`{"name": "C"}`)}, 200, &c)
	if c.Revision.Avatar == nil || *c.Revision.Avatar != blob.SHA256 || !strings.Contains(string(c.Revision.Overrides), "Lumiverse") {
		t.Fatalf("kept %+v", c.Revision)
	}
	e.call("PUT", path, map[string]any{"base_revision_id": c.Revision.ID, "data": json.RawMessage(`{"name": "D"}`), "avatar": ""}, 200, nil)
	c = entityResult{} // Unmarshal keeps fields the response omits
	e.call("GET", path, nil, 200, &c)
	if c.Revision.Avatar != nil {
		t.Fatalf("avatar not removed: %+v", c.Revision)
	}
	if !strings.Contains(string(c.Revision.Overrides), "Lumiverse") {
		t.Fatalf("overrides lost: %+v", c.Revision)
	}
	e.call("PUT", path, map[string]any{"base_revision_id": c.Revision.ID, "data": json.RawMessage(`{"name": "D2"}`),
		"overrides": nil}, 200, &c)
	if !strings.Contains(string(c.Revision.Overrides), "Lumiverse") {
		t.Fatalf("null overrides not kept: %+v", c.Revision)
	}
	e.call("PUT", path, map[string]any{"base_revision_id": c.Revision.ID, "data": json.RawMessage(`{"name": "D3"}`),
		"avatar": strings.Repeat("a", 64)}, 404, nil)
	e.call("PUT", path, map[string]any{"base_revision_id": c.Revision.ID, "data": json.RawMessage(`{"name": "E"}`),
		"overrides": json.RawMessage(`[]`)}, 400, nil)
}

func TestCardTagsLinksAndExport(t *testing.T) {
	e := newEnv(t)
	c := e.createCard(`{"name": "Brakka"}`)
	path := fmt.Sprintf("/cards/%d", c.ID)

	var tags []string
	e.call("PUT", path+"/tags", map[string]any{"tags": []string{"fantasy", "dwarf"}}, 200, &tags)
	if strings.Join(tags, ",") != "dwarf,fantasy" {
		t.Fatalf("tags = %v", tags)
	}

	var book entityResult
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Forge", "entries": [{"keys": ["anvil"], "content": "Heavy."}]}`)}, 201, &book)
	if book.Slug != "forge" || book.Tags != nil {
		t.Fatalf("lorebook = %+v", book)
	}
	e.call("PUT", fmt.Sprintf("%s/lorebooks/%d", path, book.ID), nil, 204, nil)
	e.call("PUT", fmt.Sprintf("%s/lorebooks/%d", path, 999), nil, 404, nil)

	var got entityResult
	e.call("GET", path, nil, 200, &got)
	if len(got.Tags) != 2 || len(got.Lorebooks) != 1 || got.Lorebooks[0] != book.ID {
		t.Fatalf("card = %+v", got)
	}

	r := e.call("GET", path+"/export", nil, 200, nil)
	if !strings.Contains(string(r.Body), `"character_book"`) || !strings.Contains(r.Header.Get("Content-Disposition"), "brakka.json") {
		t.Fatalf("export: %s %v", r, r.Header)
	}

	e.call("DELETE", fmt.Sprintf("%s/lorebooks/%d", path, book.ID), nil, 204, nil)
	got = entityResult{} // Unmarshal keeps fields the response omits
	e.call("GET", path, nil, 200, &got)
	if len(got.Lorebooks) != 0 {
		t.Fatalf("lorebook still linked: %+v", got)
	}

	other := e.createCard(`{"name": "Brakka"}`)
	var matches []struct {
		CardID int64  `json:"card_id"`
		Reason string `json:"reason"`
	}
	e.call("GET", path+"/similar", nil, 200, &matches)
	if len(matches) == 0 || matches[0].CardID != other.ID || matches[0].Reason != "content" {
		t.Fatalf("similar = %+v", matches)
	}
}
