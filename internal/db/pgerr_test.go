package db_test

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/inkandale/innkeeper/internal/db"
)

func TestIsCodeMatchesWrappedPgError(t *testing.T) {
	err := fmt.Errorf("insert: %w", &pgconn.PgError{Code: db.UniqueViolation})
	if !db.IsCode(err, db.UniqueViolation) {
		t.Fatal("wrapped unique violation not matched")
	}
	if db.IsCode(err, db.CheckViolation) {
		t.Fatal("unique violation matched check violation")
	}
	if db.IsCode(fmt.Errorf("plain"), db.UniqueViolation) {
		t.Fatal("non-pg error matched")
	}
}
