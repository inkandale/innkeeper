package actions

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

func TestScope(t *testing.T) {
	field := protocol.Action{ID: "tighten", Applies: "card", Fields: []string{"description"}}
	whole := protocol.Action{ID: "reforge", Applies: "card"}
	both := protocol.Action{ID: "tighten", Applies: "card", Fields: []string{"description", "scenario"}, WholeCard: true}
	base := `{"name": "Brakka", "description": "Big.", "scenario": "A cave."}`
	tests := []struct {
		name         string
		action       protocol.Action
		field, kind  string
		base         string
		proposal     string
		want         string
		wantWarnings []string
	}{
		{"field action keeps its field and drops a rename", field, "description", "card", base,
			`{"name": "Grok", "description": "Huge.", "scenario": "A cave."}`,
			`{"name": "Brakka", "description": "Huge.", "scenario": "A cave."}`, []string{"name"}},
		{"field action can't remove another field", field, "description", "card", base,
			`{"name": "Brakka", "description": "Huge."}`,
			`{"name": "Brakka", "description": "Huge.", "scenario": "A cave."}`, []string{"scenario"}},
		{"whole-card field action changes its fields and drops a rename", both, "", "card", base,
			`{"name": "Grok", "description": "Huge.", "scenario": "A pit."}`,
			`{"name": "Brakka", "description": "Huge.", "scenario": "A pit."}`, []string{"name"}},
		{"whole-card field action on one field keeps to it", both, "description", "card", base,
			`{"name": "Brakka", "description": "Huge.", "scenario": "A pit."}`,
			`{"name": "Brakka", "description": "Huge.", "scenario": "A cave."}`, []string{"scenario"}},
		{"key order and spacing are no change", field, "description", "card", base,
			`{"scenario":"A cave.","description":"Big.","name":"Brakka"}`,
			base, nil},
		{"card action changes any key", whole, "", "card", base,
			`{"name": "Grok", "description": "Huge.", "first_mes": "Hail."}`,
			`{"name": "Grok", "description": "Huge.", "first_mes": "Hail."}`, nil},
		{"a card never gets character_book", whole, "", "card", base,
			`{"name": "Brakka", "description": "Big.", "scenario": "A cave.", "character_book": {"entries": []}}`,
			base, []string{"character_book"}},
		{"a field action on character_book still can't add it", protocol.Action{ID: "book", Applies: "card", Fields: []string{"character_book"}},
			"character_book", "card", base,
			`{"name": "Brakka", "description": "Big.", "scenario": "A cave.", "character_book": {"entries": []}}`,
			base, []string{"character_book"}},
		{"report-only proposal is dropped", protocol.Action{ID: "evaluate", Applies: "card", ReportOnly: true}, "", "card", base,
			`{"name": "Grok", "description": "Big.", "scenario": "A cave."}`,
			``, []string{"name"}},
		{"lorebook action changes entries", protocol.Action{ID: "lore", Applies: "lorebook"}, "", "lorebook",
			`{"name": "North", "entries": []}`, `{"name": "North", "entries": [{"keys": ["x"]}]}`,
			`{"name": "North", "entries": [{"keys": ["x"]}]}`, nil},
		{"new action sets any key but character_book", protocol.Action{ID: "generate", Applies: "new"}, "", "card", ``,
			`{"name": "Vyr", "description": "A seer.", "character_book": {}}`,
			`{"name": "Vyr", "description": "A seer."}`, []string{"character_book"}},
		{"no proposal", whole, "", "card", base, ``, ``, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, warnings, err := Scope(tt.action, tt.field, tt.kind, raw(tt.base), raw(tt.proposal))
			if err != nil {
				t.Fatal(err)
			}
			if !sameJSON(t, got, raw(tt.want)) {
				t.Errorf("proposal = %s, want %s", got, tt.want)
			}
			if len(warnings) != len(tt.wantWarnings) {
				t.Fatalf("warnings = %q, want one per key in %q", warnings, tt.wantWarnings)
			}
			for i, key := range tt.wantWarnings {
				if !strings.Contains(warnings[i], key) {
					t.Errorf("warning %q doesn't name %s", warnings[i], key)
				}
			}
		})
	}
}

func TestScopeRejectsANonObjectProposal(t *testing.T) {
	if _, _, err := Scope(protocol.Action{Applies: "card"}, "", "card", raw(`{}`), raw(`["x"]`)); err == nil {
		t.Fatal("an array proposal passed")
	}
}

func TestPayloadCheck(t *testing.T) {
	field := protocol.Action{ID: "tighten", Applies: "card", Fields: []string{"description"}}
	both := protocol.Action{ID: "tighten", Applies: "card", Fields: []string{"description"}, WholeCard: true}
	card := protocol.ActionEntity{Kind: "card", Data: raw(`{"name": "Brakka"}`)}
	tests := []struct {
		name    string
		action  protocol.Action
		payload Payload
		wantErr string
	}{
		{"field action", field, Payload{Field: "description", Entity: card, CardID: 3}, ""},
		{"new card", protocol.Action{Applies: "new", Images: true}, Payload{Entity: protocol.ActionEntity{Kind: "card"}, Images: ImageRefs{Reference: "ab"}}, ""},
		{"card action on a lorebook", field, Payload{Field: "description", Entity: protocol.ActionEntity{Kind: "lorebook", Data: raw(`{}`)}}, "applies to card"},
		{"new with an ID", protocol.Action{Applies: "new"}, Payload{Entity: protocol.ActionEntity{Kind: "card"}, CardID: 3}, "new card"},
		{"field not offered", field, Payload{Field: "scenario", Entity: card}, "scenario"},
		{"field action without a field", field, Payload{Entity: card}, "field"},
		{"field on a whole-card action", protocol.Action{Applies: "card"}, Payload{Field: "description", Entity: card}, "field"},
		{"whole-card field action without a field", both, Payload{Entity: card}, ""},
		{"whole-card field action on its field", both, Payload{Field: "description", Entity: card}, ""},
		{"whole-card field action on another field", both, Payload{Field: "scenario", Entity: card}, "scenario"},
		{"images not taken", field, Payload{Field: "description", Entity: card, Images: ImageRefs{Avatar: "ab"}}, "images"},
		{"data not an object", protocol.Action{Applies: "card"}, Payload{Entity: protocol.ActionEntity{Kind: "card", Data: raw(`"x"`)}}, "object"},
		{"card without data", protocol.Action{Applies: "card"}, Payload{Entity: protocol.ActionEntity{Kind: "card"}}, "object"},
		{"lorebook id on a card", protocol.Action{Applies: "card"}, Payload{Entity: card, LorebookID: 2}, "lorebook"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.payload.Check(tt.action)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Check = %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check = %v, want ErrInvalid mentioning %q", err, tt.wantErr)
			}
		})
	}
}

func TestListProgressCutsResult(t *testing.T) {
	got := ListProgress(raw(`{"message": "Done", "result": {"raw": "long"}}`))
	if !sameJSON(t, got, raw(`{"message": "Done"}`)) {
		t.Fatalf("ListProgress = %s", got)
	}
}

func raw(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

func sameJSON(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return slices.Equal(ja, jb)
}

func TestLibraryCallEndsWithTheRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &session{rn: &Runner{}, ctx: ctx, action: protocol.Action{ID: "lookup", Library: true}}
	_, err := s.libraryCall(json.RawMessage(`{"name": "get_card", "arguments": {"id": 1}}`))
	var rpcErr *protocol.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != protocol.CodeForbidden {
		t.Fatalf("library.call after the run stopped: %v, want -32003", err)
	}
}
