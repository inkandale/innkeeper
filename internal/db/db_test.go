package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/db"
	"github.com/davidgibbons/innkeeper/internal/testdb"
)

func TestMigrateTwice(t *testing.T) {
	pool := testdb.New(t) // already migrated once
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migration`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("schema_migration has %d rows, want 2", n)
	}
}

func TestRevisionsAreImmutable(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var revID int64
	err := pool.QueryRow(ctx, `
		WITH c AS (INSERT INTO card (slug, provenance) VALUES ('a', 'upload') RETURNING id)
		INSERT INTO card_revision (card_id, data, content_hash, source)
		SELECT id, '{}', 'x', 'ui' FROM c RETURNING id`).Scan(&revID)
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE card_revision SET message = 'changed' WHERE id = $1`,
		`DELETE FROM card_revision WHERE id = $1`,
	} {
		_, err := pool.Exec(ctx, sql, revID)
		if err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Errorf("%s: got %v, want immutable error", sql, err)
		}
	}
}

func TestCardRejectsEmbeddedLorebook(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		WITH c AS (INSERT INTO card (slug, provenance) VALUES ('a', 'upload') RETURNING id)
		INSERT INTO card_revision (card_id, data, content_hash, source)
		SELECT id, '{"character_book": {}}', 'x', 'ui' FROM c`)
	if err == nil || !strings.Contains(err.Error(), "card_revision_data_check") {
		t.Fatalf("got %v, want card_revision_data_check violation", err)
	}
}
