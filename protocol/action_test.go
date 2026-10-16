package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestActionValidate(t *testing.T) {
	field := Action{ID: "tighten", Label: "Tighten", Applies: "card", Fields: []string{"description", "personality"},
		Inputs: json.RawMessage(`{"type": "object"}`)}
	with := func(f func(*Action)) Action {
		a := field
		a.Fields = append([]string(nil), field.Fields...)
		f(&a)
		return a
	}
	tests := []struct {
		name    string
		action  Action
		wantErr string
	}{
		{"field action", field, ""},
		{"whole lorebook", Action{ID: "lore-entries", Label: "Lore", Applies: "lorebook"}, ""},
		{"new card", Action{ID: "generate", Label: "Generate", Applies: "new", Images: true}, ""},
		{"capital id", with(func(a *Action) { a.ID = "Tighten" }), "id"},
		{"long id", with(func(a *Action) { a.ID = strings.Repeat("a", 41) }), "id"},
		{"whole-card field action", with(func(a *Action) { a.WholeCard = true }), ""},
		{"whole card without fields", Action{ID: "tighten-card", Label: "Tighten", Applies: "card", WholeCard: true}, "whole_card"},
		{"no label", with(func(a *Action) { a.Label = " " }), "label"},
		{"unknown applies", with(func(a *Action) { a.Applies = "scene" }), "applies"},
		{"fields on new", with(func(a *Action) { a.Applies = "new" }), "fields"},
		{"repeated field", with(func(a *Action) { a.Fields = []string{"description", "description"} }), "description"},
		{"empty field", with(func(a *Action) { a.Fields = []string{""} }), "field"},
		{"inputs not an object", with(func(a *Action) { a.Inputs = json.RawMessage(`{"type": "string"}`) }), "inputs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.action.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate = %v, want an error mentioning %q", err, tt.wantErr)
			}
		})
	}
}

func TestReportValidate(t *testing.T) {
	score := 78.0
	full := Report{Summary: "Solid.", Score: &score,
		Dimensions:  []ReportDimension{{Key: "voice", Label: "Voice", Score: 88, Comment: "Distinct."}},
		Findings:    []Finding{{Severity: "medium", Text: "Repeats.", Fields: []string{"scenario"}, Move: &FindingMove{Excerpt: "x", To: "description"}}},
		Suggestions: []string{"Cut the backstory."}}
	tests := []struct {
		name    string
		report  Report
		wantErr string
	}{
		{"full", full, ""},
		{"summary only", Report{Summary: "Fine."}, ""},
		{"no summary", Report{}, "summary"},
		{"bad severity", Report{Summary: "s", Findings: []Finding{{Severity: "urgent", Text: "t"}}}, "severity"},
		{"finding without text", Report{Summary: "s", Findings: []Finding{{Severity: "low"}}}, "text"},
		{"dimension without key", Report{Summary: "s", Dimensions: []ReportDimension{{Label: "Voice"}}}, "key"},
		{"move without to", Report{Summary: "s", Findings: []Finding{{Severity: "low", Text: "t", Move: &FindingMove{Excerpt: "x"}}}}, "to"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.report.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate = %v, want an error mentioning %q", err, tt.wantErr)
			}
		})
	}
}
