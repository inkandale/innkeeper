package db_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/db"
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

// undoBind takes the schema back to before 0008_bind.sql, so a test can add
// rows that it couldn't hold and then migrate again.
func undoBind(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `DROP INDEX binding_card_remote, binding_lorebook_remote;
		ALTER TABLE push DROP COLUMN kind;
		DELETE FROM schema_migration WHERE name = '0008_bind.sql'`); err != nil {
		t.Fatal(err)
	}
}

// bindings adds a card and a lorebook, and binds each to remote IDs on one
// instance. It returns the card bindings' IDs, then the lorebook's.
func bindings(t *testing.T, pool *pgxpool.Pool, cardRemotes, lorebookRemotes []string) []int64 {
	t.Helper()
	ctx := context.Background()
	var inst, card, lorebook int64
	err := pool.QueryRow(ctx, `WITH i AS (INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'Fake') RETURNING id),
		c AS (INSERT INTO card (slug, provenance) VALUES ('a', 'upload') RETURNING id),
		l AS (INSERT INTO lorebook (slug, provenance) VALUES ('b', 'upload') RETURNING id)
		SELECT i.id, c.id, l.id FROM i, c, l`).Scan(&inst, &card, &lorebook)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	add := func(col string, id int64, remote string) {
		var bid int64
		err := pool.QueryRow(ctx, `INSERT INTO binding (instance_id, `+col+`, remote_id, remote_state, accepted_data,
			accepted_hash) VALUES ($1, $2, $3, 'unchanged', '{}', 'h') RETURNING id`, inst, id, remote).Scan(&bid)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, bid)
	}
	// The binding table allows one binding per card and instance, so each
	// remote ID after the first gets its own card.
	for i, r := range cardRemotes {
		if i > 0 {
			if err := pool.QueryRow(ctx, `INSERT INTO card (slug, provenance) VALUES ($1, 'upload') RETURNING id`,
				fmt.Sprintf("a%d", i)).Scan(&card); err != nil {
				t.Fatal(err)
			}
		}
		add("card_id", card, r)
	}
	for _, r := range lorebookRemotes {
		add("lorebook_id", lorebook, r)
	}
	return ids
}

func TestBindMigrationKeepsPushes(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	undoBind(t, pool)
	b := bindings(t, pool, []string{"1", "1"}, []string{"1"})
	if _, err := pool.Exec(ctx, `INSERT INTO push (binding_id, revision_id, pushed_hash, remote_id) VALUES ($1, 1, 'h', '1')`,
		b[0]); err != nil {
		t.Fatal(err)
	}
	// A card and a lorebook may share a remote ID, and a missing binding
	// doesn't hold one.
	if _, err := pool.Exec(ctx, `UPDATE binding SET remote_state = 'missing' WHERE id = $1`, b[1]); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := pool.QueryRow(ctx, `SELECT kind FROM push`).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "push" {
		t.Fatalf("kind = %q, want push", kind)
	}
	// Two cards may not.
	_, err := pool.Exec(ctx, `WITH c AS (INSERT INTO card (slug, provenance) VALUES ('z', 'upload') RETURNING id)
		INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data, accepted_hash)
		SELECT b.instance_id, c.id, '1', 'unchanged', '{}', 'h' FROM binding b, c WHERE b.id = $1`, b[0])
	if pgErr := (*pgconn.PgError)(nil); !errors.As(err, &pgErr) || pgErr.ConstraintName != "binding_card_remote" {
		t.Fatalf("second card binding to remote ID 1: %v, want a binding_card_remote violation", err)
	}
}

func TestBindMigrationRefusesDuplicates(t *testing.T) {
	pool := testdb.New(t)
	undoBind(t, pool)
	b := bindings(t, pool, []string{"1", "1", "2"}, nil)
	err := db.Migrate(context.Background(), pool)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%d, %d", b[0], b[1])) {
		t.Fatalf("migrate = %v, want an error naming bindings %d and %d", err, b[0], b[1])
	}
}
