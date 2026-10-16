package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

var pointerEscape = strings.NewReplacer("~", "~0", "/", "~1")
var pointerUnescape = strings.NewReplacer("~1", "/", "~0", "~")

// schemaDefaults returns each "default" in a config schema by JSON pointer.
func schemaDefaults(schema map[string]any) map[string]any {
	out := map[string]any{}
	var walk func(s map[string]any, at string)
	walk = func(s map[string]any, at string) {
		if d, ok := s["default"]; ok && at != "" {
			out[at] = d
		}
		props, _ := s["properties"].(map[string]any)
		for k, p := range props {
			if ps, ok := p.(map[string]any); ok {
				walk(ps, at+"/"+pointerEscape.Replace(k))
			}
		}
	}
	walk(schema, "")
	return out
}

// lookup returns the value at a JSON pointer into config.
func lookup(config map[string]any, pointer string) (any, bool) {
	var cur any = config
	for _, tok := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[pointerUnescape.Replace(tok)]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// hashDefault fingerprints a default value. json.Marshal sorts map keys, so
// equal values hash the same.
func hashDefault(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// checkBases refuses a base whose key isn't a JSON pointer, which lookup
// would otherwise read as one.
func checkBases(bases map[string]any) error {
	for p := range bases {
		if !strings.HasPrefix(p, "/") {
			return fmt.Errorf("%w: default_bases key %q is not a JSON pointer", ErrInvalid, p)
		}
	}
	return nil
}

// mergeDefaultHashes is the hash of the default each override in config
// replaced: bases for overrides the owner just set, current for the rest.
// A pointer config no longer overrides is dropped.
func mergeDefaultHashes(current map[string]string, config map[string]any, bases map[string]any) map[string]string {
	out := map[string]string{}
	for p, h := range current {
		if _, ok := lookup(config, p); ok {
			out[p] = h
		}
	}
	for p, d := range bases {
		if _, ok := lookup(config, p); ok {
			out[p] = hashDefault(d)
		}
	}
	return out
}
