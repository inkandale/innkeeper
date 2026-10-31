package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnrichmentCheck(t *testing.T) {
	text := Enrichment{Type: "text", Label: "Summary"}
	score := Enrichment{Type: "score", Label: "Quality", Range: []float64{0, 100}}
	list := Enrichment{Type: "list", Label: "Themes"}
	tests := []struct {
		e     Enrichment
		value string
		ok    bool
	}{
		{text, `"Fine."`, true},
		{text, `78`, false},
		{text, `null`, false},
		{score, `0`, true},
		{score, `100`, true},
		{score, `78.5`, true},
		{score, `101`, false},
		{score, `-1`, false},
		{score, `"78"`, false},
		{score, `null`, false},
		{list, `["a", "b"]`, true},
		{list, `[]`, true},
		{list, `["a", 1]`, false},
		{list, `"a"`, false},
		{list, `null`, false},
		{text, `"a\u0000b"`, false},
		{list, `["ok", "\u0000"]`, false},
		{text, "\"\xff\"", false},
		{text, `"` + strings.Repeat("x", MaxEnrichValue) + `"`, false},
		{text, `"\\u0000 is fine escaped"`, true},
	}
	for _, tt := range tests {
		if err := tt.e.Check(json.RawMessage(tt.value)); (err == nil) != tt.ok {
			t.Errorf("%s Check(%s) = %v, want ok %v", tt.e.Type, tt.value, err, tt.ok)
		}
	}
}

func TestCheckMeta(t *testing.T) {
	for meta, ok := range map[string]bool{
		`{"model": "m"}`:      true,
		`"m"`:                 false,
		`{"model": "\u0000"}`: false,
		`{"x": "` + strings.Repeat("x", MaxEnrichMeta) + `"}`: false,
	} {
		if err := CheckMeta(json.RawMessage(meta)); (err == nil) != ok {
			t.Errorf("CheckMeta(%.40s) = %v, want ok %v", meta, err, ok)
		}
	}
}

func TestCheckValuesDropsUndeclaredAndInvalid(t *testing.T) {
	decl := map[string]Enrichment{
		"rating": {Type: "score", Label: "Rating", Range: []float64{0, 5}},
		"stars":  {Type: "score", Label: "Stars", Range: []float64{0, 5}},
	}
	kept, problems := CheckValues(decl, map[string]json.RawMessage{
		"rating": json.RawMessage(`4.5`),
		"stars":  json.RawMessage(`9`),
		"views":  json.RawMessage(`120`),
	})
	if len(kept) != 1 || string(kept["rating"]) != `4.5` {
		t.Fatalf("kept = %v", kept)
	}
	want := []string{"dropped stars: 9 is outside 0 to 5", "dropped views, which the plugin doesn't declare"}
	if strings.Join(problems, "|") != strings.Join(want, "|") {
		t.Fatalf("problems = %q, want %q", problems, want)
	}
}

func TestVectorEnrichmentCheck(t *testing.T) {
	d := Enrichment{Type: "vector", Label: "Embedding"}
	for value, ok := range map[string]bool{
		`{"space": "m1", "vector": [0.5, -1]}`: true,
		`{"space": "", "vector": [1]}`:         false,
		`{"space": "m1", "vector": []}`:        false,
		`{"space": "m1"}`:                      false,
		`[1, 2]`:                               false,
		`"text"`:                               false,
		`{"space": "m1", "vector": null}`:      false,
		`{"space": "m1", "vector": [1e39]}`:    false,
	} {
		if err := d.Check(json.RawMessage(value)); (err == nil) != ok {
			t.Errorf("Check(%s) = %v, want ok %v", value, err, ok)
		}
	}
	long := `{"space": "` + strings.Repeat("x", 201) + `", "vector": [1]}`
	if d.Check(json.RawMessage(long)) == nil {
		t.Error("a 201-character space passed")
	}
}
