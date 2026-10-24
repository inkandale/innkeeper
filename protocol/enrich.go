package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
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
	// Thorough asks for the plugin's most careful values, not its fast bulk path.
	Thorough bool `json:"thorough,omitempty"`
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

// Size limits on what enrich.run returns.
const (
	MaxEnrichValue = 64 << 10
	MaxEnrichMeta  = 16 << 10
)

// storable reports why PostgreSQL's jsonb would refuse raw: invalid UTF-8,
// or a NUL in a string.
func storable(raw json.RawMessage, limit int) error {
	if len(raw) > limit {
		return fmt.Errorf("it is over %d KiB", limit>>10)
	}
	if !utf8.Valid(raw) {
		return errors.New("it isn't valid UTF-8")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	if hasNUL(v) {
		return errors.New("it holds a NUL character")
	}
	return nil
}

func hasNUL(v any) bool {
	switch v := v.(type) {
	case string:
		return strings.ContainsRune(v, 0)
	case []any:
		return slices.ContainsFunc(v, hasNUL)
	case map[string]any:
		for k, x := range v {
			if strings.ContainsRune(k, 0) || hasNUL(x) {
				return true
			}
		}
	}
	return false
}

// CheckMeta reports whether meta is an object core can store.
func CheckMeta(meta json.RawMessage) error {
	if err := storable(meta, MaxEnrichMeta); err != nil {
		return err
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(meta, &obj) != nil || obj == nil {
		return errors.New("it isn't an object")
	}
	return nil
}

// Check reports whether v is a valid value for e: a string for text, a
// number within the range for score, and an array of strings for list, at
// most MaxEnrichValue bytes, that PostgreSQL can store.
func (e Enrichment) Check(v json.RawMessage) error {
	if err := storable(v, MaxEnrichValue); err != nil {
		return err
	}
	switch e.Type {
	case "text":
		var s *string
		if json.Unmarshal(v, &s) != nil || s == nil {
			return errors.New("it must be a string")
		}
	case "score":
		var n *float64
		if json.Unmarshal(v, &n) != nil || n == nil {
			return errors.New("it must be a number")
		}
		if *n < e.Range[0] || *n > e.Range[1] {
			return fmt.Errorf("%v is outside %v to %v", *n, e.Range[0], e.Range[1])
		}
	case "list":
		var l []string
		if json.Unmarshal(v, &l) != nil || l == nil {
			return errors.New("it must be a list of strings")
		}
	default:
		return fmt.Errorf("unknown type %q", e.Type)
	}
	return nil
}

// CheckValues keeps the values decl declares with a valid value, and says
// why it dropped each of the others, in key order.
func CheckValues(decl map[string]Enrichment, values map[string]json.RawMessage) (map[string]json.RawMessage, []string) {
	kept := map[string]json.RawMessage{}
	var problems []string
	for _, key := range slices.Sorted(maps.Keys(values)) {
		d, ok := decl[key]
		if !ok {
			problems = append(problems, fmt.Sprintf("dropped %s, which the plugin doesn't declare", key))
			continue
		}
		if err := d.Check(values[key]); err != nil {
			problems = append(problems, fmt.Sprintf("dropped %s: %v", key, err))
			continue
		}
		kept[key] = values[key]
	}
	return kept, problems
}
