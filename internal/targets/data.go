package targets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/davidgibbons/innkeeper/internal/cardhash"
)

// Change is one top-level field the app changed.
type Change struct {
	Field string `json:"field"`
	// Before is absent if the field was empty at the last push or adopt.
	Before json.RawMessage `json:"before,omitempty"`
	// After is absent if the app cleared the field.
	After json.RawMessage `json:"after,omitempty"`
}

// MergePatch applies an RFC 7386 JSON merge patch to doc. Per-target
// overrides are merge patches applied to a card's data.
func MergePatch(doc, patch json.RawMessage) (json.RawMessage, error) {
	d, err := decode(doc)
	if err != nil {
		return nil, err
	}
	p, err := decode(patch)
	if err != nil {
		return nil, err
	}
	return json.Marshal(mergePatch(d, p))
}

func mergePatch(doc, patch any) any {
	p, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	d, ok := doc.(map[string]any)
	if !ok {
		d = map[string]any{}
	}
	for k, v := range p {
		if v == nil {
			delete(d, k)
		} else {
			d[k] = mergePatch(d[k], v)
		}
	}
	return d
}

// decode keeps numbers as written, so large integers survive.
func decode(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}

func object(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("not a JSON object")
	}
	return m, nil
}

// cardData returns a CCv3 card's data object.
func cardData(card json.RawMessage) (json.RawMessage, error) {
	var c struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(card, &c); err != nil {
		return nil, err
	}
	if _, err := object(c.Data); err != nil {
		return nil, fmt.Errorf("card data: %w", err)
	}
	return c.Data, nil
}

// project keeps only the fields a target stores, and hashes the result, so
// fields the app drops never count as changes.
func project(data json.RawMessage, fields []string) (json.RawMessage, string, error) {
	all, err := object(data)
	if err != nil {
		return nil, "", err
	}
	kept := map[string]json.RawMessage{}
	for _, f := range fields {
		if v, ok := all[f]; ok {
			kept[f] = v
		}
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		return nil, "", err
	}
	hash, err := cardhash.ContentHash(raw)
	return raw, hash, err
}

// valueHash hashes one field's value as the content hash normalizes it, so
// whitespace and empty values compare the way the drift hash does.
func valueHash(v json.RawMessage) (string, error) {
	if v == nil {
		v = json.RawMessage("null")
	}
	return cardhash.ContentHash(fmt.Appendf(nil, `{"v": %s}`, v))
}

// diff lists the top-level fields that differ between two data objects, by
// name.
func diff(before, after json.RawMessage) ([]Change, error) {
	b, err := object(before)
	if err != nil {
		return nil, err
	}
	a, err := object(after)
	if err != nil {
		return nil, err
	}
	keys := slices.Sorted(maps.Keys(b))
	for k := range a {
		if _, ok := b[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	var out []Change
	for _, k := range keys {
		hb, err := valueHash(b[k])
		if err != nil {
			return nil, err
		}
		ha, err := valueHash(a[k])
		if err != nil {
			return nil, err
		}
		if hb != ha {
			out = append(out, Change{Field: k, Before: b[k], After: a[k]})
		}
	}
	return out, nil
}

// droppedFields lists the fields with content that a target doesn't store.
func droppedFields(data json.RawMessage, fields []string) ([]string, error) {
	all, err := object(data)
	if err != nil {
		return nil, err
	}
	empty, err := valueHash(nil)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, k := range slices.Sorted(maps.Keys(all)) {
		if slices.Contains(fields, k) {
			continue
		}
		if h, err := valueHash(all[k]); err != nil {
			return nil, err
		} else if h != empty {
			out = append(out, k)
		}
	}
	return out, nil
}

// adoptable leaves out character_book: lorebook changes can't be adopted yet,
// because the card's lorebooks are separate entities.
func adoptable(changes []Change) []Change {
	return slices.DeleteFunc(slices.Clone(changes), func(c Change) bool { return c.Field == "character_book" })
}

// applyChanges sets each changed field of data to the app's value, or
// removes it where the app cleared it. It reports whether it changed
// anything.
func applyChanges(data json.RawMessage, changes []Change) (json.RawMessage, bool, error) {
	changes = adoptable(changes)
	if len(changes) == 0 {
		return data, false, nil
	}
	all, err := object(data)
	if err != nil {
		return nil, false, err
	}
	for _, c := range changes {
		if c.After == nil {
			delete(all, c.Field)
		} else {
			all[c.Field] = c.After
		}
	}
	out, err := json.Marshal(all)
	return out, true, err
}

// overrideChanges adds the changes to the override for key, so the target
// gets the app's values while the card's own data stays as it is.
func overrideChanges(overrides json.RawMessage, key string, changes []Change) (json.RawMessage, bool, error) {
	changes = adoptable(changes)
	if len(changes) == 0 {
		return overrides, false, nil
	}
	all, err := object(overrides)
	if err != nil {
		return nil, false, err
	}
	patch := map[string]json.RawMessage{}
	if p, ok := all[key]; ok {
		if patch, err = object(p); err != nil {
			return nil, false, fmt.Errorf("override %s: %w", key, err)
		}
	}
	for _, c := range changes {
		if c.After == nil {
			patch[c.Field] = json.RawMessage("null")
		} else {
			patch[c.Field] = c.After
		}
	}
	if all[key], err = json.Marshal(patch); err != nil {
		return nil, false, err
	}
	out, err := json.Marshal(all)
	return out, true, err
}
