package db_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/db"
	"github.com/inkandale/innkeeper/internal/testdb"
)

func TestEnableVectorsCreatesTheTable(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := db.EnableVectors(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableVectors(ctx, pool); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if ok, err := db.VectorsAvailable(ctx, pool); err != nil || !ok {
		t.Fatalf("available = %v, %v", ok, err)
	}
}

// A role that may not create extensions stands in for PostgreSQL without
// pgvector: both make CREATE EXTENSION fail.
func TestEnableVectorsWithoutPgvectorTurnsThemOff(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS card_embedding; DROP EXTENSION IF EXISTS vector`); err != nil {
		t.Fatal(err)
	}
	role := fmt.Sprintf("novec%d", time.Now().UnixNano())
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD 'x'; GRANT ALL ON SCHEMA public TO %s`, role, role)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP OWNED BY "+role+"; DROP ROLE "+role) })
	cfg, err := pgxpool.ParseConfig(os.Getenv("INNKEEPER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.User, cfg.ConnConfig.Password = role, "x"
	cfg.ConnConfig.Database = pool.Config().ConnConfig.Database
	plain, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(plain.Close)

	if err := db.EnableVectors(ctx, plain); !errors.Is(err, db.ErrNoVectors) {
		t.Fatalf("EnableVectors = %v, want ErrNoVectors", err)
	}
	if ok, err := db.VectorsAvailable(ctx, plain); err != nil || ok {
		t.Fatalf("available = %v, %v", ok, err)
	}
}
