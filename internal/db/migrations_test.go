package db_test

import (
	"context"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/testdb"
)

func TestDeletingInstanceKeepsItsJobs(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var inst, job int64
	if err := pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'Fake') RETURNING id`).Scan(&inst); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO job (kind, instance_id) VALUES ('import', $1) RETURNING id`, inst).Scan(&job); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM plugin_instance WHERE id = $1`, inst); err != nil {
		t.Fatal(err)
	}
	var got *int64
	if err := pool.QueryRow(ctx, `SELECT instance_id FROM job WHERE id = $1`, job).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("instance_id = %d, want null", *got)
	}
}

func TestDeletingParentJobDeletesChildren(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var parent int64
	if err := pool.QueryRow(ctx, `INSERT INTO job (kind, status) VALUES ('bulk', 'succeeded') RETURNING id`).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO job (kind, parent_id, status) VALUES ('card.archive', $1, 'succeeded')`, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM job WHERE id = $1`, parent); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d jobs left, want 0", n)
	}
}

func TestCardRevisionSearchColumn(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var match bool
	err := pool.QueryRow(ctx, `
		WITH c AS (INSERT INTO card (slug, provenance) VALUES ('a', 'upload') RETURNING id),
		r AS (INSERT INTO card_revision (card_id, data, content_hash, source)
			SELECT id, '{"name": "Brakka", "description": "A dwarf blacksmith."}', 'x', 'ui' FROM c RETURNING search)
		SELECT search @@ websearch_to_tsquery('english', 'blacksmiths') FROM r`).Scan(&match)
	if err != nil {
		t.Fatal(err)
	}
	if !match {
		t.Fatal("search column doesn't match a stemmed word from the description")
	}
}

func TestDeletingAuthInstanceEndsSessions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var inst int64
	if err := pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name, owner_subject) VALUES ('fake', 'Login', 'owner') RETURNING id`).Scan(&inst); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO session (token_hash, instance_id, subject, expires_at) VALUES ('h1', $1, 'owner', now())`, inst); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO api_token (name, token_hash) VALUES ('cli', 'h2')`); err != nil {
		t.Fatal(err)
	}
	// Deleting the auth instance ends its sessions.
	if _, err := pool.Exec(ctx, `DELETE FROM plugin_instance WHERE id = $1`, inst); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM session`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d sessions left, want 0", n)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_token`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d API tokens left, want 1", n)
	}
}
