package targets

import "testing"

// markBehind stands in for the resolve code that sets app_behind.
func (e *env) markBehind() {
	e.t.Helper()
	if _, err := e.pool.Exec(e.ctx, `UPDATE push SET app_behind = TRUE
		WHERE id IN (SELECT max(id) FROM push GROUP BY binding_id)`); err != nil {
		e.t.Fatal(err)
	}
}

func TestAppBehindMakesCardAheadUntilNextPush(t *testing.T) {
	e := setup(t, nil)
	e.push(e.card)
	e.markBehind()
	if s := e.status(e.card); s != "ahead" {
		t.Fatalf("app_behind: %s, want ahead", s)
	}
	if b := e.push(e.card); b.Status != "in_sync" {
		t.Fatalf("after a push: %s, want in_sync", b.Status)
	}
}

func TestAppBehindMakesLorebookAheadUntilNextPush(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("Forge")
	e.pushLorebook(lb)
	e.markBehind()
	if s := e.lorebookStatus(lb); s != "ahead" {
		t.Fatalf("app_behind: %s, want ahead", s)
	}
	if b := e.pushLorebook(lb); b.Status != "in_sync" {
		t.Fatalf("after a push: %s, want in_sync", b.Status)
	}
}
