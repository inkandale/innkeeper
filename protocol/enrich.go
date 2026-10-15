package protocol

import (
	"encoding/json"
	"fmt"
)

// The enrich capability computes values about card revisions, such as quality
// scores, for the keys the manifest's [enrichments] declares.
const MethodEnrichRun = "enrich.run"

// MaxEnrichBatch is the most revisions one enrich.run carries.
const MaxEnrichBatch = 20

// EnrichRevision is one revision to enrich. Data is CCv3 data without
// character_book.
type EnrichRevision struct {
	CardID     int64           `json:"card_id"`
	RevisionID int64           `json:"revision_id"`
	Data       json.RawMessage `json:"data"`
}

// EnrichRunParams asks for values for up to MaxEnrichBatch revisions.
type EnrichRunParams struct {
	Run       string           `json:"run"`
	Revisions []EnrichRevision `json:"revisions"`
}

// EnrichResult is one revision's values, or why it has none.
type EnrichResult struct {
	RevisionID int64                      `json:"revision_id"`
	Values     map[string]json.RawMessage `json:"values,omitempty"`
	Meta       json.RawMessage            `json:"meta,omitempty"`
	Error      string                     `json:"error,omitempty"`
}

// EnrichRunResult is the reply to enrich.run.
type EnrichRunResult struct {
	Results []EnrichResult `json:"results"`
}

// Check reports whether v is a valid value for e: a string for text, a
// number within the range for score, and an array of strings for list.
func (e Enrichment) Check(v json.RawMessage) error {
	switch e.Type {
	case "text":
		var s *string
		if json.Unmarshal(v, &s) != nil || s == nil {
			return fmt.Errorf("%s must be a string", v)
		}
	case "score":
		var n *float64
		if json.Unmarshal(v, &n) != nil || n == nil {
			return fmt.Errorf("%s must be a number", v)
		}
		if *n < e.Range[0] || *n > e.Range[1] {
			return fmt.Errorf("%v is outside %v to %v", *n, e.Range[0], e.Range[1])
		}
	case "list":
		var l []string
		if json.Unmarshal(v, &l) != nil || l == nil {
			return fmt.Errorf("%s must be a list of strings", v)
		}
	default:
		return fmt.Errorf("unknown type %q", e.Type)
	}
	return nil
}
