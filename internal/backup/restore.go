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
	Type            Type             `json:"type"`
	Rows            map[string]int64 `json:"rows"`
	Blobs           int              `json:"blobs"`
	SkippedBindings []int64          `json:"skipped_bindings,omitempty"`
	// Schemas whose plugin has no database role here, so nothing could own them.
	SkippedPluginSchemas []string `json:"skipped_plugin_schemas,omitempty"`
	SecretsKeyMismatch   bool     `json:"secrets_key_mismatch,omitempty"`
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validSHA(s string) bool { return shaPattern.MatchString(s) }

var schemaPattern = regexp.MustCompile(`^plugin_[a-z0-9_]+$`)

func validSchema(s string) bool { return schemaPattern.MatchString(s) }

// unrunnable matches the psql meta-commands pg_dump 18 adds, which the server
// can't run, and the CREATE SCHEMA that restore runs itself.
var unrunnable = regexp.MustCompile(`(?m)^(\\(un)?restrict \S+|CREATE SCHEMA \S+;)$`)

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
	// Its own connection, since plugin SQL changes session settings.
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
	// briefly removes rows the other type's tables point to. innkeeper.restore
	// lets a replace delete revisions.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('innkeeper-restore'));
		SET CONSTRAINTS ALL DEFERRED; SET LOCAL innkeeper.restore = 'on'`); err != nil {
		return Result{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, hasData[m.Type]).Scan(&exists); err != nil {
		return Result{}, err
	}
	if exists && !o.Replace {
		return Result{}, ErrHasData
	}
	if err := clearType(ctx, tx, m.Type, exists); err != nil {
		return Result{}, fmt.Errorf("clear %s: %w", m.Type, err)
	}

	res := Result{Type: m.Type, Rows: map[string]int64{}}
	seenBlobs, seenSchemas := map[string]bool{}, map[string]bool{}
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
		case strings.HasPrefix(name, "plugins/") && strings.HasSuffix(name, ".sql"):
			schema := strings.TrimSuffix(strings.TrimPrefix(name, "plugins/"), ".sql")
			if !validSchema(schema) || !slices.Contains(m.PluginSchemas, schema) || seenSchemas[schema] {
				return res, fmt.Errorf("unexpected archive entry %q", name)
			}
			seenSchemas[schema] = true
			b, err := io.ReadAll(tr)
			if err != nil {
				return res, fmt.Errorf("reading %s: %w", name, err)
			}
			ok, err := loadPlugin(ctx, tx, schema, string(unrunnable.ReplaceAll(b, nil)))
			if err != nil {
				return res, fmt.Errorf("%s: %w", name, err)
			}
			if !ok {
				res.SkippedPluginSchemas = append(res.SkippedPluginSchemas, schema)
			}
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
	if len(seenSchemas) != len(m.PluginSchemas) {
		return res, fmt.Errorf("archive has %d of the %d plugin schemas its manifest lists", len(seenSchemas), len(m.PluginSchemas))
	}
	res.Blobs = len(seenBlobs)

	// Plugin SQL empties search_path, so everything from here names public.
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
			if pe.TableName == "binding" {
				return res, fmt.Errorf("bindings refer to cards or lorebooks this backup doesn't have; restore a matching config backup or unbind them first: %w", err)
			}
			return res, fmt.Errorf("backup refers to rows it doesn't contain: %w", err)
		}
		return res, err
	}
	return res, nil
}

// clearType deletes type t's rows when the database has them, leaving the
// other type's tables alone. Config always drops the plugin schemas, which it
// owns: a start creates empty ones even without instances.
func clearType(ctx context.Context, tx pgx.Tx, t Type, exists bool) error {
	var stmts []string
	if exists {
		switch t {
		case Assets:
			for _, table := range slices.Backward(tables[Assets]) {
				stmts = append(stmts, `DELETE FROM `+pgx.Identifier{"public", table}.Sanitize())
			}
		case Config:
			stmts = []string{`DELETE FROM public.push`, `DELETE FROM public.binding`, `DELETE FROM public.setting`, `DELETE FROM public.plugin_instance`}
		}
	}
	if t == Config {
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

// loadPlugin runs a plugin schema's SQL as the plugin's own role, so an
// uploaded archive can't touch core tables. It returns false when this
// database has no such role. A security-definer function runs it because
// SET ROLE alone can be undone by the SQL with RESET ROLE; inside one,
// PostgreSQL refuses any role change.
func loadPlugin(ctx context.Context, tx pgx.Tx, schema, sql string) (bool, error) {
	var role string
	var member bool
	if err := tx.QueryRow(ctx, `SELECT current_database() || '_' || $1::text,
		coalesce((SELECT pg_has_role(current_user, oid, 'MEMBER') FROM pg_roles WHERE rolname = current_database() || '_' || $1::text), false)`,
		schema).Scan(&role, &member); err != nil {
		return false, err
	}
	if !member {
		return false, nil
	}
	s, r := pgx.Identifier{schema}.Sanitize(), pgx.Identifier{role}.Sanitize()
	run := pgx.Identifier{schema, "innkeeper_restore"}.Sanitize()
	for _, q := range []string{
		`DROP SCHEMA IF EXISTS ` + s + ` CASCADE`,
		`CREATE SCHEMA ` + s + ` AUTHORIZATION ` + r,
		`SET LOCAL ROLE ` + r,
		`CREATE FUNCTION ` + run + `(q text) RETURNS void LANGUAGE plpgsql SECURITY DEFINER AS $$BEGIN EXECUTE q; END$$`,
		`RESET ROLE`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, `SELECT `+run+`($1)`, sql); err != nil {
		return false, err
	}
	_, err := tx.Exec(ctx, `DROP FUNCTION IF EXISTS `+run+`(pg_catalog.text)`)
	return true, err
}

// loadTable copies one table's rows from r. Bindings and pushes go through
// temp tables so ones for cards or lorebooks this database lacks are skipped;
// blobs do so that rows awaiting cleanup don't collide.
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
	if table == "binding" || table == "push" || table == "blob" {
		dst = pgx.Identifier{"pg_temp", "restore_" + table}.Sanitize()
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE `+dst+` (LIKE `+pgx.Identifier{"public", table}.Sanitize()+`) ON COMMIT DROP`); err != nil {
			return 0, err
		}
	}
	tag, err := tx.Conn().PgConn().CopyFrom(ctx, r, fmt.Sprintf("COPY %s (%s) FROM STDIN", dst, list))
	if err != nil {
		return 0, err
	}
	switch table {
	case "blob":
		tag, err = tx.Exec(ctx, `INSERT INTO public.blob (`+list+`) SELECT `+list+` FROM pg_temp.restore_blob ON CONFLICT (sha256) DO NOTHING`)
	case "binding":
		rows, _ := tx.Query(ctx, `SELECT id FROM pg_temp.restore_binding WHERE NOT (`+bindable+`) ORDER BY id`)
		if res.SkippedBindings, err = pgx.CollectRows(rows, pgx.RowTo[int64]); err != nil {
			return 0, err
		}
		tag, err = tx.Exec(ctx, `INSERT INTO public.binding (`+list+`) OVERRIDING SYSTEM VALUE
			SELECT `+list+` FROM pg_temp.restore_binding WHERE `+bindable)
	case "push":
		tag, err = tx.Exec(ctx, `INSERT INTO public.push (`+list+`) OVERRIDING SYSTEM VALUE
			SELECT `+list+` FROM pg_temp.restore_push WHERE binding_id IN (SELECT id FROM pg_temp.restore_binding WHERE `+bindable+`)`)
	}
	return tag.RowsAffected(), err
}
