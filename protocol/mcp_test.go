package protocol

import (
	"encoding/json"
	"testing"
)

func TestTool(t *testing.T) {
	obj := json.RawMessage(`{"type": "object"}`)
	for _, c := range []struct {
		tool  Tool
		valid bool
		tier  string
	}{
		{Tool{Name: "list_untracked", InputSchema: obj, Tier: "read"}, true, "read"},
		{Tool{Name: "wipe", InputSchema: obj}, true, "destructive"},
		{Tool{Name: "odd", InputSchema: obj, Tier: "admin"}, true, "destructive"},
		{Tool{Name: "Bad-Name", InputSchema: obj}, false, "destructive"},
		{Tool{Name: "noschema", InputSchema: json.RawMessage(`{"type": "string"}`)}, false, "destructive"},
	} {
		if err := c.tool.Validate(); (err == nil) != c.valid {
			t.Errorf("%s: Validate = %v", c.tool.Name, err)
		}
		if got := c.tool.EffectiveTier(); got != c.tier {
			t.Errorf("%s: tier %s, want %s", c.tool.Name, got, c.tier)
		}
	}
}
