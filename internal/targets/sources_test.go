package targets

import (
	"testing"

	"github.com/davidgibbons/innkeeper/internal/db"
	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestPullOnlyBindingsMayShareAnItem(t *testing.T) {
	e := setup(t, nil)
	insert := func(card int64, push bool) error {
		_, err := e.pool.Exec(e.ctx, `INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data,
			accepted_hash, push) VALUES ($1, $2, 'shared', 'unknown', '{}', '', $3)`, e.inst, card, push)
		return err
	}
	if err := insert(e.card, false); err != nil {
		t.Fatal(err)
	}
	if err := insert(e.newCard("Tova"), false); err != nil {
		t.Fatalf("second pull-only binding on one item: %v", err)
	}
	if err := insert(e.newCard("Pell"), true); err != nil {
		t.Fatalf("first push binding on the item: %v", err)
	}
	if err := insert(e.newCard("Iko"), true); !db.IsCode(err, db.UniqueViolation) {
		t.Fatalf("second push binding on one item = %v, want a unique violation", err)
	}
}

func TestAPullOnlyBindingIsNeverAhead(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	if _, err := e.pool.Exec(e.ctx, `UPDATE binding SET push = false WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A dwarf smith, retired.") })
	if got := e.status(e.card); got != "in_sync" {
		t.Fatalf("status = %s, want in_sync", got)
	}
}
