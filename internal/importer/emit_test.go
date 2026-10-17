package importer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
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

func TestAutosyncWaitsForASyncWithoutStallingTheImport(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.run(t, `{"cards": 2}`)
	card := e.slugID(t, "card", "fake-0")
	c, _ := e.lib.Cards.Get(ctx, card)
	if _, err := e.lib.Cards.Update(ctx, card, c.HeadRevisionID, library.Edit{Source: "ui",
		Data: json.RawMessage(`{"name": "Fake 0", "description": "Card number 0.", "personality": "Bold."}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE binding SET autosync = true WHERE card_id = $1 AND imported`, card); err != nil {
		t.Fatal(err)
	}
	// A running sync holds this lock, and applying an update waits for it.
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended('innkeeper-sync', 0))`); err != nil {
		t.Fatal(err)
	}
	j, err := e.q.Enqueue(ctx, jobs.Spec{Kind: Kind, InstanceID: &e.inst, Payload: Payload{Params: json.RawMessage(`{"cards": 2, "text": " Again."}`)}})
	if err != nil {
		t.Fatal(err)
	}
	// The card after the pending one is stored and checkpointed while the sync runs.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		got, err := e.q.Get(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Checkpoint) == `{"next": 2}` || string(got.Checkpoint) == `{"next":2}` {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("import stalled behind the sync at checkpoint %s", got.Checkpoint)
		}
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended('innkeeper-sync', 0))`); err != nil {
		t.Fatal(err)
	}
	if done := e.wait(t, j.ID); done.Status != jobs.StatusSucceeded {
		t.Fatalf("job %s: %s", done.Status, errText(done))
	}
	c, _ = e.lib.Cards.Get(ctx, card)
	head, _ := e.lib.Cards.Revision(ctx, c.HeadRevisionID)
	if !strings.Contains(string(head.Data), "Again.") || !strings.Contains(string(head.Data), "Bold.") {
		t.Fatalf("autosync didn't merge the update: %s", head.Data)
	}
}

func TestBulkBatchThatFailsToCommitIsNotCounted(t *testing.T) {
	e := setup(t, "-mode=bulk")
	j, p := e.run(t, `{"cards": 6, "checkpoint_every": 3, "nul_checkpoint": 6}`)
	if j.Status != jobs.StatusFailed {
		t.Fatalf("job %s, want failed on the checkpoint it can't store", j.Status)
	}
	if n := e.count(t, "card"); p.Imported != int64(n) {
		t.Fatalf("progress counts %d imported, but %d cards were stored", p.Imported, n)
	}
}

func TestBulkImportSkipsAnOlderVersionSentLater(t *testing.T) {
	e := setup(t, "-mode=bulk")
	e.run(t, `{"cards": 2, "created": "2024-03-01T00:00:00Z"}`)
	j, p := e.run(t, `{"cards": 2, "text": " Older.", "created": "2024-01-01T00:00:00Z"}`)
	if j.Status != jobs.StatusSucceeded || p.Skipped != 2 || p.Revised != 0 {
		t.Fatalf("job %s, progress %+v", j.Status, p)
	}
	if n := e.count(t, "card_revision"); n != 2 {
		t.Fatalf("%d revisions, want 2", n)
	}
}
