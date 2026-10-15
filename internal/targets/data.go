package targets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/davidgibbons/innkeeper/cardhash"
)

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
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after JSON value")
	}
	return v, nil
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

// cardData returns a CCv3 card's data object without the app-local
// extensions the library drops, so they never count as changes. It keeps
// character_book, which embedded lorebooks are read from.
func cardData(card json.RawMessage) (json.RawMessage, error) {
	data, book, err := cardhash.CardData(card)
	if err != nil {
		return nil, fmt.Errorf("card data: %w", err)
	}
	if data == nil {
		return nil, errors.New("card data: not a JSON object")
	}
	if book == nil {
		return data, nil
	}
	m, err := object(data)
	if err != nil {
		return nil, err
	}
	m["character_book"] = book
	return json.Marshal(m)
}

// Project keeps only the fields a target stores, and hashes the result, so
// fields the app drops never count as changes.
func Project(data json.RawMessage, fields []string) (json.RawMessage, string, error) {
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

// valueHash hashes one field as the content hash normalizes it, so
// whitespace, empty values, and volatile fields compare the way the drift hash does.
func valueHash(field string, v json.RawMessage) (string, error) {
	raw, err := json.Marshal(map[string]json.RawMessage{field: v})
	if err != nil {
		return "", err
	}
	return cardhash.ContentHash(raw)
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
	keys := slices.Collect(maps.Keys(b))
	for k := range a {
		if _, ok := b[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	var out []Change
	for _, k := range keys {
		hb, err := valueHash(k, b[k])
		if err != nil {
			return nil, err
		}
		ha, err := valueHash(k, a[k])
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
	out := []string{}
	for _, k := range slices.Sorted(maps.Keys(all)) {
		if slices.Contains(fields, k) {
			continue
		}
		empty, err := valueHash(k, nil)
		if err != nil {
			return nil, err
		}
		if h, err := valueHash(k, all[k]); err != nil {
			return nil, err
		} else if h != empty {
			out = append(out, k)
		}
	}
	return out, nil
}
