package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type entityResult struct {
	ID          int64          `json:"id"`
	Slug        string         `json:"slug"`
	Provenance  string         `json:"provenance"`
	ArchivedAt  *string        `json:"archived_at"`
	Tags        []string       `json:"tags"`
	Lorebooks   []int64        `json:"lorebooks"`
	Collections []int64        `json:"collections"`
	Cards       []int64        `json:"cards"`
	Revision    revisionResult `json:"revision"`
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

func TestRestoreAvatar(t *testing.T) {
	e := newEnv(t)
	blob, err := e.s.Lib.PutBlob(context.Background(), strings.NewReader("avatar"))
	if err != nil {
		t.Fatal(err)
	}
	c := e.createCard(`{"name": "Old"}`)
	first := c.Revision.ID
	path := fmt.Sprintf("/cards/%d", c.ID)
	e.call("PUT", path, map[string]any{"base_revision_id": first, "data": json.RawMessage(`{"name": "New"}`), "avatar": blob.SHA256}, 200, &c)

	e.call("POST", path+"/restore", map[string]any{"revision_id": first, "base_revision_id": c.Revision.ID, "avatar": "nope"}, 422, nil)
	for _, avatar := range []string{"", "keep", "revision"} {
		body := map[string]any{"revision_id": first, "base_revision_id": c.Revision.ID}
		if avatar != "" {
			body["avatar"] = avatar
		}
		c = entityResult{}
		e.call("POST", path+"/restore", body, 200, &c)
		if !strings.Contains(string(c.Revision.Data), "Old") {
			t.Fatalf("%q: data not restored: %s", avatar, c.Revision.Data)
		}
		if kept := c.Revision.Avatar != nil && *c.Revision.Avatar == blob.SHA256; kept != (avatar != "revision") {
			t.Fatalf("%q: avatar = %v", avatar, c.Revision.Avatar)
		}
		if avatar == "revision" {
			break
		}
		// Put the new text back so the next restore changes something.
		e.call("PUT", path, map[string]any{"base_revision_id": c.Revision.ID, "data": json.RawMessage(`{"name": "New"}`)}, 200, &c)
	}
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

func TestExportFormats(t *testing.T) {
	e := newEnv(t)
	brakka := e.createCard(`{"name": "Brakka"}`)
	path := fmt.Sprintf("/cards/%d/export", brakka.ID)

	// newEnv's fake instances are codecs that write the format "fake".
	r := e.call("GET", path+"?format=fake", nil, 200, nil)
	if !strings.HasPrefix(string(r.Body), "fake:") || !strings.Contains(string(r.Body), `"Brakka"`) ||
		!strings.Contains(r.Header.Get("Content-Disposition"), "brakka.txt") ||
		r.Header.Get("Content-Type") != "application/octet-stream" ||
		r.Header.Get("X-Content-Type-Options") != "nosniff" || r.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("fake export: %s %v", r, r.Header)
	}
	r = e.call("GET", path, nil, 200, nil)
	if r.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("json export Content-Type = %q", r.Header.Get("Content-Type"))
	}
	e.call("GET", path+"?format=nope", nil, 400, nil)

	// The spec lists the raw file type, not huma's base64 string for []byte.
	var spec struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Content map[string]any `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(e.send("GET", "/openapi.json", nil).Body, &spec); err != nil {
		t.Fatal(err)
	}
	if _, ok := spec.Paths["/cards/{id}/export"]["get"].Responses["200"].Content["application/octet-stream"]; !ok {
		t.Fatalf("export 200 content = %v", spec.Paths["/cards/{id}/export"]["get"].Responses["200"].Content)
	}

	// The fake writes a card named Escape outside blob_tmp.
	escape := e.createCard(`{"name": "Escape"}`)
	e.call("GET", fmt.Sprintf("/cards/%d/export?format=fake", escape.ID), nil, 502, nil)

	// The fake names this text file .png. Its type comes from its content.
	mislabel := e.createCard(`{"name": "Mislabel"}`)
	r = e.call("GET", fmt.Sprintf("/cards/%d/export?format=fake", mislabel.ID), nil, 200, nil)
	if r.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("mislabeled export Content-Type = %q", r.Header.Get("Content-Type"))
	}

	e.markBroken(e.loginID, "boom")
	e.markBroken(e.importID, "boom")
	e.call("GET", path+"?format=fake", nil, 400, nil)
}

func TestCardCollectionsField(t *testing.T) {
	e := newEnv(t)
	c := e.createCard(`{"name": "Brakka"}`)
	var coll struct {
		ID int64 `json:"id"`
	}
	e.call("POST", "/collections", map[string]any{"name": "Favorites"}, 201, &coll)
	e.call("PUT", fmt.Sprintf("/collections/%d/cards/%d", coll.ID, c.ID), nil, 204, nil)
	var got entityResult
	e.call("GET", fmt.Sprintf("/cards/%d", c.ID), nil, 200, &got)
	if len(got.Collections) != 1 || got.Collections[0] != coll.ID {
		t.Fatalf("collections = %v", got.Collections)
	}
}

func TestCompareRevisions(t *testing.T) {
	e := newEnv(t)
	c := e.createCard(`{"name": "Brakka", "description": "A dwarf. Grumpy."}`)
	path := fmt.Sprintf("/cards/%d", c.ID)
	var updated entityResult
	e.call("PUT", path, map[string]any{"base_revision_id": c.Revision.ID,
		"data": json.RawMessage(`{"name": "Brakka", "description": "A dwarf. Cheerful."}`)}, 200, &updated)

	var diff struct {
		Fields []struct {
			Path  string `json:"path"`
			Hunks []struct {
				Status  string `json:"status"`
				Library string `json:"library"`
				App     string `json:"app"`
			} `json:"hunks"`
		} `json:"fields"`
	}
	e.call("GET", fmt.Sprintf("%s/revisions/%d/compare/%d", path, c.Revision.ID, updated.Revision.ID), nil, 200, &diff)
	if len(diff.Fields) != 2 || diff.Fields[0].Path != "description" || diff.Fields[1].Path != "name" {
		t.Fatalf("fields = %+v", diff.Fields)
	}
	changed := false
	for _, h := range diff.Fields[0].Hunks {
		if h.Status != "same" && strings.Contains(h.Library, "Grumpy") && strings.Contains(h.App, "Cheerful") {
			changed = true
		}
	}
	if !changed || diff.Fields[1].Hunks[0].Status != "same" {
		t.Fatalf("hunks = %+v", diff.Fields)
	}

	other := e.createCard(`{"name": "Other"}`)
	e.call("GET", fmt.Sprintf("%s/revisions/%d/compare/%d", path, c.Revision.ID, other.Revision.ID), nil, 404, nil)

	var book entityResult
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Forge", "entries": [{"keys": ["anvil"], "content": "Heavy."}]}`)}, 201, &book)
	var edited entityResult
	e.call("PUT", fmt.Sprintf("/lorebooks/%d", book.ID), map[string]any{"base_revision_id": book.Revision.ID,
		"data": json.RawMessage(`{"name": "Forge", "entries": [{"keys": ["anvil"], "content": "Very heavy."}]}`)}, 200, &edited)
	var bookDiff struct {
		Fields []struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		} `json:"fields"`
	}
	e.call("GET", fmt.Sprintf("/lorebooks/%d/revisions/%d/compare/%d", book.ID, book.Revision.ID, edited.Revision.ID), nil, 200, &bookDiff)
	// Entries diff per entry only with the lorebook schema; the card schema would treat them as one scalar.
	kinds := map[string]string{}
	for _, f := range bookDiff.Fields {
		kinds[f.Path] = f.Kind
	}
	if kinds["entries"] != "entries" {
		t.Fatalf("lorebook fields = %+v", bookDiff.Fields)
	}
}

func TestLorebookCardsField(t *testing.T) {
	e := newEnv(t)
	c := e.createCard(`{"name": "Brakka"}`)
	var book entityResult
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Forge", "entries": []}`)}, 201, &book)
	e.call("PUT", fmt.Sprintf("/cards/%d/lorebooks/%d", c.ID, book.ID), nil, 204, nil)
	var got entityResult
	e.call("GET", fmt.Sprintf("/lorebooks/%d", book.ID), nil, 200, &got)
	if len(got.Cards) != 1 || got.Cards[0] != c.ID {
		t.Fatalf("cards = %v", got.Cards)
	}
	var card entityResult
	e.call("GET", fmt.Sprintf("/cards/%d", c.ID), nil, 200, &card)
	if card.Cards != nil {
		t.Fatalf("a card lists cards: %v", card.Cards)
	}
}
