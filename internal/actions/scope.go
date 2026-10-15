package actions

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/davidgibbons/innkeeper/protocol"
)

// Scope keeps the changes proposal makes to base that action a may make, and
// returns a warning per key it dropped. field is the run's field, and kind
// the entity's. A nil base is a new card's empty draft. A report-only
// action's proposal is dropped whole.
func Scope(a protocol.Action, field, kind string, base, proposal json.RawMessage) (json.RawMessage, []string, error) {
	if len(proposal) == 0 || string(proposal) == "null" {
		return nil, nil, nil
	}
	after, err := object(proposal)
	if err != nil {
		return nil, nil, errors.New("proposal must be a JSON object")
	}
	before := map[string]json.RawMessage{}
	if len(base) > 0 {
		if before, err = object(base); err != nil {
			return nil, nil, err
		}
	}
	allowed := func(key string) bool {
		switch {
		case a.ReportOnly:
			return false
		case len(a.Fields) > 0:
			return key == field
		}
		// Linked lorebooks hold a card's book; the library refuses one inside the card.
		return kind != protocol.ApplyCard || key != "character_book"
	}
	union := maps.Clone(before)
	maps.Copy(union, after)
	keys := slices.Sorted(maps.Keys(union))
	var warnings []string
	for _, k := range keys {
		old, had := before[k]
		v, has := after[k]
		if had == has && (!had || sameValue(old, v)) || allowed(k) {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("dropped the change to %s: outside the action's scope", k))
		if had {
			after[k] = old
		} else {
			delete(after, k)
		}
	}
	if a.ReportOnly {
		return nil, warnings, nil
	}
	out, err := json.Marshal(after)
	return out, warnings, err
}

// object decodes a JSON object, refusing null and other values.
func object(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, errors.New("not a JSON object")
	}
	return m, nil
}

// sameValue compares JSON values, ignoring key order and spacing.
func sameValue(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
