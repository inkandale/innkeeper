package protocol

import (
	"encoding/json"
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
	}
	for _, tt := range tests {
		if err := tt.e.Check(json.RawMessage(tt.value)); (err == nil) != tt.ok {
			t.Errorf("%s Check(%s) = %v, want ok %v", tt.e.Type, tt.value, err, tt.ok)
		}
	}
}
