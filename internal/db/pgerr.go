package db

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL SQLSTATE codes the core maps to its own errors.
const (
	ForeignKeyViolation = "23503"
	UniqueViolation     = "23505"
	CheckViolation      = "23514"
)

// IsDataError reports whether PostgreSQL refused err's statement for the
// data it was given, such as text it can't store or a value over a limit,
// so the same data fails every time.
func IsDataError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (strings.HasPrefix(pgErr.Code, "22") || strings.HasPrefix(pgErr.Code, "54"))
}

// IsCode reports whether err wraps a PostgreSQL error with SQLSTATE code.
func IsCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
