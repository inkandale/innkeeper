package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/secrets"
)

var ErrHasData = errors.New("this database already has data of that type; restore with replace to overwrite it")

type RestoreOptions struct {
	Conn    *pgx.ConnConfig // restore opens its own connection
	Store   *blobstore.Store
	Box     *secrets.Box
	Replace bool
}

type Result struct {
	Type               Type             `json:"type"`
	Rows               map[string]int64 `json:"rows"`
	Blobs              int              `json:"blobs"`
	SkippedBindings    []int64          `json:"skipped_bindings,omitempty"`
	SecretsKeyMismatch bool             `json:"secrets_key_mismatch,omitempty"`
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validSHA(s string) bool { return shaPattern.MatchString(s) }

// restrictLine matches the psql meta-commands pg_dump 18 adds, which the
// server can't run.
var restrictLine = regexp.MustCompile(`(?m)^\\(un)?restrict \S+$`)

// hasData finds rows a restore without replace would collide with.
var hasData = map[Type]string{
	Assets: `SELECT EXISTS (SELECT FROM public.card) OR EXISTS (SELECT FROM public.lorebook)
		OR EXISTS (SELECT FROM public.tag) OR EXISTS (SELECT FROM public.collection)`,
	Config: `SELECT EXISTS (SELECT FROM public.plugin_instance) OR EXISTS (SELECT FROM public.setting)`,
}

// bindable keeps bindings whose card or lorebook this database has.
const bindable = `(card_id IS NULL OR EXISTS (SELECT FROM public.card WHERE id = card_id))
	AND (lorebook_id IS NULL OR EXISTS (SELECT FROM public.lorebook WHERE id = lorebook_id))`

// Restore loads an archive of one backup type in a single transaction. Blobs
// are written to the store as they arrive; a failed restore leaves only
// unreferenced files, which blob cleanup removes.
func Restore(ctx context.Context, r io.Reader, o RestoreOptions) (Result, error) {
	tr, m, err := openArchive(r)
	if err != nil {
		return Result{}, err
	}
	// Its own connection, since plugins.sql changes session settings.
	conn, err := pgx.ConnectConfig(ctx, o.Conn)
	if err != nil {
		return Result{}, err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	tx, err := conn.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	var schema string
	if err := tx.QueryRow(ctx, `SELECT max(name) FROM public.schema_migration`).Scan(&schema); err != nil {
		return Result{}, err
	}
	if m.Schema != schema {
		return Result{}, fmt.Errorf("backup was made by another Innkeeper version (schema %s, this database %s)", m.Schema, schema)
	}
	// Deferred: card and card_revision reference each other, and a replace
	// briefly removes rows the other type's tables point to.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('innkeeper-restore')); SET CONSTRAINTS ALL DEFERRED`); err != nil {
		return Result{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, hasData[m.Type]).Scan(&exists); err != nil {
		return Result{}, err
	}
	if exists && !o.Replace {
		return Result{}, ErrHasData
	}
	if exists {
		if err := clearType(ctx, tx, m.Type); err != nil {
			return Result{}, fmt.Errorf("clear %s: %w", m.Type, err)
		}
	}

	res := Result{Type: m.Type, Rows: map[string]int64{}}
	seenBlobs := map[string]bool{}
	plugins := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return res, fmt.Errorf("reading archive: %w", err)
		}
		name := h.Name
		switch {
		case strings.HasPrefix(name, "blobs/"):
			sha := strings.TrimPrefix(name, "blobs/")
			if !validSHA(sha) || !slices.Contains(m.Blobs, sha) || seenBlobs[sha] {
				return res, fmt.Errorf("unexpected archive entry %q", name)
			}
			got, _, err := o.Store.Put(tr)
			if err != nil {
				return res, fmt.Errorf("%s: %w", name, err)
			}
			if got != sha {
				return res, fmt.Errorf("%s: content hashes to %s", name, got)
			}
			seenBlobs[sha] = true
		case strings.HasPrefix(name, "tables/") && strings.HasSuffix(name, ".copy"):
			table := strings.TrimSuffix(strings.TrimPrefix(name, "tables/"), ".copy")
			_, listed := m.Tables[table]
			if _, dup := res.Rows[table]; dup || !listed || !slices.Contains(tables[m.Type], table) {
				return res, fmt.Errorf("unexpected archive entry %q", name)
			}
			if res.Rows[table], err = loadTable(ctx, tx, table, tr, &res); err != nil {
				return res, fmt.Errorf("%s: %w", table, err)
			}
		case name == "plugins.sql" && m.Type == Config && len(m.PluginSchemas) > 0 && !plugins:
			b, err := io.ReadAll(tr)
			if err != nil {
				return res, fmt.Errorf("reading plugins.sql: %w", err)
			}
			// No arguments, so pgx sends it as one multi-statement query.
			if _, err := tx.Exec(ctx, string(restrictLine.ReplaceAll(b, nil))); err != nil {
				return res, fmt.Errorf("plugins.sql: %w", err)
			}
			plugins = true
		default:
			return res, fmt.Errorf("unexpected archive entry %q", name)
		}
	}
	for table := range m.Tables {
		if _, ok := res.Rows[table]; !ok {
			return res, fmt.Errorf("archive is missing tables/%s.copy", table)
		}
	}
	if len(seenBlobs) != len(m.Blobs) {
		return res, fmt.Errorf("archive has %d of the %d blobs its manifest lists", len(seenBlobs), len(m.Blobs))
	}
	if len(m.PluginSchemas) > 0 && !plugins {
		return res, errors.New("archive is missing plugins.sql")
	}
	res.Blobs = len(seenBlobs)

	// plugins.sql empties search_path, so everything from here names public.
	// Without this, the next insert would reuse a restored ID.
	rows, _ := tx.Query(ctx, `SELECT table_name::text FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'id' AND is_identity = 'YES' AND table_name = ANY($1)`, tables[m.Type])
	identity, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return res, err
	}
	for _, table := range identity {
		t := pgx.Identifier{"public", table}.Sanitize()
		if _, err := tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence($1, 'id'), coalesce((SELECT max(id) FROM `+t+`), 0) + 1, false)`, t); err != nil {
			return res, fmt.Errorf("%s sequence: %w", table, err)
		}
	}
	if m.Type == Config {
		// Running plugin processes restart with the restored settings.
		if _, err := tx.Exec(ctx, `UPDATE public.plugin_instance SET updated_at = now()`); err != nil {
			return res, err
		}
		res.SecretsKeyMismatch = m.SecretsCheck != "" && o.Box != nil && m.SecretsCheck != o.Box.Derive("backup-check")
	}
	if err := tx.Commit(ctx); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23503" {
			return res, fmt.Errorf("bindings refer to cards or lorebooks this backup doesn't have; restore a matching config backup or unbind them first: %w", err)
		}
		return res, err
	}
	return res, nil
}

// clearType deletes type t's rows, leaving the other type's tables alone.
func clearType(ctx context.Context, tx pgx.Tx, t Type) error {
	var stmts []string
	switch t {
	case Assets:
		// PostgreSQL won't re-enable a trigger on a table with pending deferred
		// checks, so foreign keys into the revision tables are checked
		// immediately, after the cards and lorebooks pointing at them are gone.
		var immediate string
		if err := tx.QueryRow(ctx, `SELECT 'SET CONSTRAINTS ' || string_agg(format('public.%I', conname), ', ') || ' IMMEDIATE'
			FROM pg_constraint WHERE contype = 'f' AND confrelid IN ('public.card_revision'::regclass, 'public.lorebook_revision'::regclass)`).Scan(&immediate); err != nil {
			return err
		}
		stmts = []string{
			`ALTER TABLE public.card_revision DISABLE TRIGGER card_revision_immutable`,
			`ALTER TABLE public.lorebook_revision DISABLE TRIGGER lorebook_revision_immutable`,
			`DELETE FROM public.lorebook_source`, `DELETE FROM public.card_source`, `DELETE FROM public.collection_card`,
			`DELETE FROM public.collection`, `DELETE FROM public.card_tag`, `DELETE FROM public.tag`,
			`DELETE FROM public.card_lorebook`, `DELETE FROM public.lorebook`, `DELETE FROM public.card`,
			immediate,
			`DELETE FROM public.lorebook_revision`, `DELETE FROM public.card_revision`,
			`ALTER TABLE public.card_revision ENABLE TRIGGER card_revision_immutable`,
			`ALTER TABLE public.lorebook_revision ENABLE TRIGGER lorebook_revision_immutable`,
			`SET CONSTRAINTS ALL DEFERRED`,
			`DELETE FROM public.blob`,
		}
	case Config:
		stmts = []string{`DELETE FROM public.push`, `DELETE FROM public.binding`, `DELETE FROM public.setting`, `DELETE FROM public.plugin_instance`}
		rows, _ := tx.Query(ctx, `SELECT nspname::text FROM pg_namespace WHERE nspname LIKE 'plugin\_%'`)
		schemas, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, s := range schemas {
			stmts = append(stmts, `DROP SCHEMA `+pgx.Identifier{s}.Sanitize()+` CASCADE`)
		}
	}
	for _, q := range stmts {
		if _, err := tx.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// loadTable copies one table's rows from r. Bindings and pushes go through
// temp tables so ones for cards or lorebooks this database lacks are skipped.
func loadTable(ctx context.Context, tx pgx.Tx, table string, r io.Reader, res *Result) (int64, error) {
	cols, err := Columns(ctx, tx, table)
	if err != nil {
		return 0, err
	}
	for i, c := range cols {
		cols[i] = pgx.Identifier{c}.Sanitize()
	}
	list := strings.Join(cols, ", ")
	dst := pgx.Identifier{"public", table}.Sanitize()
	if table == "binding" || table == "push" {
		dst = pgx.Identifier{"restore_" + table}.Sanitize()
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE `+dst+` (LIKE `+pgx.Identifier{"public", table}.Sanitize()+`) ON COMMIT DROP`); err != nil {
			return 0, err
		}
	}
	tag, err := tx.Conn().PgConn().CopyFrom(ctx, r, fmt.Sprintf("COPY %s (%s) FROM STDIN", dst, list))
	if err != nil {
		return 0, err
	}
	switch table {
	case "binding":
		rows, _ := tx.Query(ctx, `SELECT id FROM restore_binding WHERE NOT (`+bindable+`) ORDER BY id`)
		if res.SkippedBindings, err = pgx.CollectRows(rows, pgx.RowTo[int64]); err != nil {
			return 0, err
		}
		tag, err = tx.Exec(ctx, `INSERT INTO public.binding (`+list+`) OVERRIDING SYSTEM VALUE
			SELECT `+list+` FROM restore_binding WHERE `+bindable)
	case "push":
		tag, err = tx.Exec(ctx, `INSERT INTO public.push (`+list+`) OVERRIDING SYSTEM VALUE
			SELECT `+list+` FROM restore_push WHERE binding_id IN (SELECT id FROM restore_binding WHERE `+bindable+`)`)
	}
	return tag.RowsAffected(), err
}
