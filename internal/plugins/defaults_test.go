package plugins

import (
	"maps"
	"testing"
)

func TestSchemaDefaultsFindsNestedDefaults(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"url":     map[string]any{"type": "string", "default": "http://localhost"},
		"a/b":     map[string]any{"type": "integer", "default": int64(3)},
		"prompts": map[string]any{"type": "object", "properties": map[string]any{"greet": map[string]any{"type": "string", "default": "Hi"}}},
		"model":   map[string]any{"type": "string"},
	}}
	got := schemaDefaults(schema)
	want := map[string]any{"/url": "http://localhost", "/a~1b": int64(3), "/prompts/greet": "Hi"}
	if !maps.Equal(got, want) {
		t.Fatalf("defaults = %v, want %v", got, want)
	}
}

func TestLookupFollowsPointer(t *testing.T) {
	config := map[string]any{"prompts": map[string]any{"greet": "Yo"}, "a/b": 1.0}
	if v, ok := lookup(config, "/prompts/greet"); !ok || v != "Yo" {
		t.Errorf("/prompts/greet = %v, %v", v, ok)
	}
	if v, ok := lookup(config, "/a~1b"); !ok || v != 1.0 {
		t.Errorf("/a~1b = %v, %v", v, ok)
	}
	if _, ok := lookup(config, "/prompts/missing"); ok {
		t.Error("found a missing key")
	}
	if _, ok := lookup(config, "/prompts/greet/deeper"); ok {
		t.Error("walked into a string")
	}
}

func TestMergeDefaultHashesKeepsOldBasesForUntouchedOverrides(t *testing.T) {
	current := map[string]string{"/p/a": hashDefault("old A"), "/p/gone": hashDefault("x")}
	config := map[string]any{"p": map[string]any{"a": "mine", "b": "mine too"}}
	got := mergeDefaultHashes(current, config, map[string]any{"/p/b": "default B", "/p/none": "y"})
	want := map[string]string{"/p/a": hashDefault("old A"), "/p/b": hashDefault("default B")}
	if !maps.Equal(got, want) {
		t.Fatalf("hashes = %v, want %v", got, want)
	}
}

func TestHashDefaultIgnoresKeyOrder(t *testing.T) {
	if hashDefault(map[string]any{"a": 1, "b": 2}) != hashDefault(map[string]any{"b": 2, "a": 1}) {
		t.Fatal("key order changed the hash")
	}
}
