package api

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestVariantRoutes(t *testing.T) {
	e := newEnv(t)
	parent := e.createCard(`{"name": "Brakka", "tags": ["dwarf"]}`)
	var v entityOut
	e.call("POST", fmt.Sprintf("/cards/%d/variants", parent.ID), map[string]any{}, 201, &v)
	if v.Slug != "brakka-variant" || v.VariantOf == nil || v.VariantOf.ID != parent.ID || v.VariantOf.Name != "Brakka" || v.Revision.Source != "ui" {
		t.Fatalf("fork = %+v", v)
	}
	var got entityOut
	e.call("GET", fmt.Sprintf("/cards/%d", parent.ID), nil, 200, &got)
	if got.VariantOf != nil || len(got.Variants) != 1 || got.Variants[0].ID != v.ID || got.Variants[0].Slug != "brakka-variant" {
		t.Fatalf("parent = %+v", got)
	}

	link := fmt.Sprintf("/cards/%d/variant_of", parent.ID)
	e.call("PUT", link, map[string]any{"id": v.ID}, 409, nil)
	e.call("PUT", link, map[string]any{"id": 999999}, 404, nil)
	e.call("DELETE", fmt.Sprintf("/cards/%d/variant_of", v.ID), nil, 204, nil)
	e.call("PUT", link, map[string]any{"id": v.ID}, 204, nil)
	e.call("GET", fmt.Sprintf("/cards/%d", parent.ID), nil, 200, &got)
	if got.VariantOf == nil || got.VariantOf.ID != v.ID {
		t.Fatalf("after relinking = %+v", got.VariantOf)
	}
}

func TestLorebookVariantRoutes(t *testing.T) {
	e := newEnv(t)
	var lb entityOut
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Forge"}`)}, 201, &lb)
	var v entityOut
	e.call("POST", fmt.Sprintf("/lorebooks/%d/variants", lb.ID), map[string]any{"source": "mcp"}, 201, &v)
	if v.Slug != "forge-variant" || v.VariantOf == nil || v.VariantOf.ID != lb.ID || v.Revision.Source != "mcp" {
		t.Fatalf("fork = %+v", v)
	}
}
