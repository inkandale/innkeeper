package importer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/jobs"
)

func TestEmitCardFieldsThroughTheBroker(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	j, p := e.run(t, `{"cards": 1, "version": "v3", "created": "2024-03-01T12:00:00Z",
		"values": {"quality": 87, "voice": 140, "views": 12}}`)
	if j.Status != jobs.StatusSucceeded || p.Imported != 1 {
		t.Fatalf("job %s, progress %+v", j.Status, p)
	}
	c, err := e.lib.Cards.Get(ctx, e.slugID(t, "card", "fake-0"))
	if err != nil {
		t.Fatal(err)
	}
	rev, err := e.lib.Cards.Revision(ctx, c.HeadRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Message != "v3" || !rev.CreatedAt.Equal(time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("revision = %+v", rev)
	}
	vals, err := e.lib.Enrichments(ctx, c.ID, false)
	if err != nil || len(vals) != 1 || vals[0].Key != "quality" || vals[0].InstanceID != e.inst || string(vals[0].Value) != "87" {
		t.Fatalf("enrichments = %+v, %v", vals, err)
	}
	// voice is out of its declared range and views isn't declared.
	if len(p.Warnings) != 2 {
		t.Fatalf("warnings = %q", p.Warnings)
	}
}

func TestImportJobRevisesTheSameCard(t *testing.T) {
	e := setup(t)
	e.run(t, `{"cards": 2}`)
	j, p := e.run(t, `{"cards": 2, "text": " Now with more.", "version": "v2"}`)
	if j.Status != jobs.StatusSucceeded || p.Revised != 2 || p.Imported != 0 {
		t.Fatalf("job %s, progress %+v", j.Status, p)
	}
	if n := e.count(t, "card"); n != 2 {
		t.Fatalf("%d cards, want 2", n)
	}
	if n := e.count(t, "card_revision"); n != 4 {
		t.Fatalf("%d revisions, want 4", n)
	}
}

func TestImportJobTargetsItsLibrary(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var archive int64
	if err := e.pool.QueryRow(ctx, `INSERT INTO library (name) VALUES ('Archive') RETURNING id`).Scan(&archive); err != nil {
		t.Fatal(err)
	}
	j, _ := e.runPayload(t, Payload{Params: json.RawMessage(`{"cards": 2}`), Library: archive})
	if j.Status != jobs.StatusSucceeded {
		t.Fatalf("job %s: %s", j.Status, errText(j))
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM card WHERE library_id = $1`, archive).Scan(&n); err != nil || n != 2 {
		t.Fatalf("%d cards in the archive, %v", n, err)
	}
}

func TestBulkImportResumesWithoutDuplicates(t *testing.T) {
	e := setup(t, "-mode=bulk")
	// A checkpoint follows every third card; the crash after the fifth loses the two sent since one.
	j, p := e.run(t, `{"cards": 8, "checkpoint_every": 3, "crash_at": 4, "avatar": true}`)
	if j.Status != jobs.StatusSucceeded || j.Attempts != 2 {
		t.Fatalf("job %s after %d attempts: %s", j.Status, j.Attempts, errText(j))
	}
	if e.count(t, "card") != 8 || e.count(t, "card_revision") != 8 {
		t.Fatalf("%d cards, %d revisions; want 8 each", e.count(t, "card"), e.count(t, "card_revision"))
	}
	// The uncommitted batch was never counted, so nothing was skipped.
	if p.Imported != 8 || p.Skipped != 0 {
		t.Fatalf("progress %+v", p)
	}
	if n := e.count(t, "source_detect"); n != 0 {
		t.Fatalf("bulk import queued %d entities for detection", n)
	}
}
