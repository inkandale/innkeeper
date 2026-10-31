package enrich

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/protocol"
)

func TestCheck(t *testing.T) {
	decl := map[string]protocol.Enrichment{
		"quality": {Type: "score", Label: "Quality", Range: []float64{0, 100}},
		"summary": {Type: "text", Label: "Summary"},
	}
	rev := func(id int64, name string) protocol.EnrichRevision {
		return protocol.EnrichRevision{CardID: id * 10, RevisionID: id, Data: json.RawMessage(`{"name": "` + name + `"}`)}
	}
	sent := []protocol.EnrichRevision{rev(1, "Good"), rev(2, "Bad"), rev(3, "Failing"), rev(4, "Skipped"), rev(5, "Junk")}
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	reply := protocol.EnrichRunResult{Results: []protocol.EnrichResult{
		{RevisionID: 1, Values: map[string]json.RawMessage{"quality": raw("78"), "summary": raw(`"Fine."`)}, Meta: raw(`{"model": "m"}`)},
		{RevisionID: 2, Values: map[string]json.RawMessage{"quality": raw("150"), "summary": raw(`"Ok."`), "mood": raw(`"grim"`)}, Meta: raw(`"m"`)},
		{RevisionID: 3, Error: "the model returned no JSON"},
		{RevisionID: 5, Values: map[string]json.RawMessage{"summary": raw("7")}},
		{RevisionID: 9, Values: map[string]json.RawMessage{"quality": raw("1")}},
		{RevisionID: 1, Values: map[string]json.RawMessage{"quality": raw("1")}},
	}}
	got := Check(decl, sent, reply)

	var stored []string
	for _, v := range got.Values {
		stored = append(stored, v.Key+"="+string(v.Value)+"@"+string(v.Meta))
		if v.CardID != v.RevisionID*10 {
			t.Errorf("value %s on card %d, revision %d", v.Key, v.CardID, v.RevisionID)
		}
	}
	want := []string{`quality=78@{"model": "m"}`, `summary="Fine."@{"model": "m"}`, `summary="Ok."@`}
	if !slices.Equal(stored, want) {
		t.Errorf("stored %q, want %q", stored, want)
	}
	if got.Done != 2 || got.Failed != 3 {
		t.Errorf("done %d, failed %d; want 2 and 3", got.Done, got.Failed)
	}
	wantFailed := []library.RevisionRef{{CardID: 30, RevisionID: 3}, {CardID: 50, RevisionID: 5}, {CardID: 40, RevisionID: 4}}
	if !slices.Equal(got.FailedRevisions, wantFailed) {
		t.Errorf("failed revisions %v, want %v", got.FailedRevisions, wantFailed)
	}
	for _, w := range []string{"Bad: dropped meta: it isn't an object", "Bad: dropped mood, which the plugin doesn't declare", "Bad: dropped quality: 150 is outside",
		"Failing: the model returned no JSON", "Junk: dropped summary", "Junk: no valid values", "Skipped: the plugin sent no result",
		"revision 9", "revision 1"} {
		if !slices.ContainsFunc(got.Warnings, func(s string) bool { return strings.Contains(s, w) }) {
			t.Errorf("no warning with %q in %q", w, got.Warnings)
		}
	}
}

func TestCheckCutsLongPluginErrors(t *testing.T) {
	sent := []protocol.EnrichRevision{{CardID: 1, RevisionID: 1, Data: json.RawMessage(`{"name": "Long"}`)}}
	got := Check(nil, sent, protocol.EnrichRunResult{Results: []protocol.EnrichResult{{RevisionID: 1, Error: strings.Repeat("é", 1000)}}})
	if len(got.Warnings) != 1 || len(got.Warnings[0]) > maxWarning+len("Long: …") {
		t.Fatalf("warnings = %d bytes", len(got.Warnings[0]))
	}
}
