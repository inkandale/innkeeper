package api

import (
	"encoding/json"
	"testing"

	"github.com/inkandale/innkeeper/internal/maintenance"
	"github.com/inkandale/innkeeper/internal/tier"
)

func TestMaintenanceRoutes(t *testing.T) {
	e := newEnv(t)
	var p maintenance.Preview
	e.call("GET", "/maintenance?keep=3&days=30", nil, 200, &p)
	if p.DatabaseBytes == 0 || len(p.Largest) == 0 {
		t.Fatalf("preview %+v", p)
	}
	e.call("GET", "/maintenance?keep=0", nil, 422, nil)
	e.call("GET", "/maintenance?days=0", nil, 422, nil)
	var j jobOut
	e.call("POST", "/maintenance/runs", map[string]any{"action": "clear-failures"}, 202, &j)
	var again jobOut
	e.call("POST", "/maintenance/runs", map[string]any{"action": "vacuum"}, 202, &again)
	if again.ID != j.ID {
		t.Fatal("a second maintenance job started while one was queued")
	}
	// Omitted keep and days take the defaults, never zero.
	var got maintenance.Payload
	var raw []byte
	if err := e.pool.QueryRow(t.Context(), `SELECT payload FROM job WHERE id = $1`, j.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil || got != (maintenance.Payload{Action: "clear-failures", Keep: 3, Days: 30}) {
		t.Fatalf("payload %s: %+v, %v", raw, got, err)
	}
	e.call("POST", "/maintenance/runs", map[string]any{"action": "nope"}, 422, nil)
	for _, bad := range []map[string]any{
		{"action": "purge", "days": 0},
		{"action": "purge", "days": -1},
		{"action": "trim-history", "keep": 0},
		{"action": "trim-history", "keep": 101},
	} {
		e.call("POST", "/maintenance/runs", bad, 422, nil)
	}
}

func TestMaintenanceRefusesAWriteToken(t *testing.T) {
	e := newEnv(t)
	auth := []string{"Authorization", "Bearer " + e.tokenWith(tier.Write)}
	if r := e.send("GET", "/maintenance", nil, auth...); r.Status != 403 {
		t.Errorf("GET: %s", r)
	}
	if r := e.send("POST", "/maintenance/runs", map[string]any{"action": "vacuum"}, auth...); r.Status != 403 {
		t.Errorf("POST: %s", r)
	}
}
