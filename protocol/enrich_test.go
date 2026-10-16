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
