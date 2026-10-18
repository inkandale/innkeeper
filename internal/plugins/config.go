package plugins

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/inkandale/innkeeper/protocol"
)

// checkConfig checks config against m's config schema, except for required:
// saved config leaves out defaults, and a missing key makes the instance
// needs_setup, not invalid.
func checkConfig(m *protocol.Manifest, config map[string]any) error {
	if len(m.Config) == 0 {
		return nil
	}
	raw, err := json.Marshal(withoutRequired(m.Config))
	if err != nil {
		return err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return fmt.Errorf("plugin %s config schema: %w", m.Name, err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return fmt.Errorf("plugin %s config schema: %w", m.Name, err)
	}
	if config == nil {
		config = map[string]any{}
	}
	// Validate wants JSON values: float64 numbers, not the API's or TOML's ints.
	raw, err = json.Marshal(config)
	if err != nil {
		return err
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if err := resolved.Validate(value); err != nil {
		return fmt.Errorf("%w: config: %v", ErrInvalid, err)
	}
	return nil
}

// withoutRequired copies a schema with every required list removed. A
// property named required is a schema object, not a list, so it stays.
func withoutRequired(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			if _, isList := x.([]any); k == "required" && isList {
				continue
			}
			out[k] = withoutRequired(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = withoutRequired(x)
		}
		return out
	}
	return v
}
