// Package backup writes, lists, prunes and restores scheduled backups of the
// asset store and the app configuration.
package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/secrets"
)

// Type is a kind of backup. Each type has its own schedule and retention.
type Type string

const (
	Assets Type = "assets"
	Config Type = "config"
)

var Types = []Type{Assets, Config}

// tables lists each type's tables in load order. Jobs, tokens, sessions and
// OAuth grants are never backed up.
var tables = map[Type][]string{
	Assets: {"blob", "card", "card_revision", "lorebook", "lorebook_revision", "card_lorebook",
		"collection", "collection_card", "card_source", "lorebook_source", "enrichment"},
	Config: {"plugin_instance", "setting", "binding", "push"},
}

// referencedBlobs limits the blob copy to avatars of revisions. Revisions are
// never deleted, so their files can't vanish mid-backup.
const referencedBlobs = `sha256 IN (SELECT avatar_blob FROM card_revision UNION SELECT avatar_blob FROM lorebook_revision)`

const timeFormat = "20060102T150405.000000Z"

// Manifest is a backup's manifest.json, written last.
type Manifest struct {
	Format        int              `json:"format"`
	Type          Type             `json:"type"`
	CreatedAt     time.Time        `json:"created_at"`
	Schema        string           `json:"schema"` // latest applied migration
	Tables        map[string]int64 `json:"tables"` // rows per table
	Blobs         []string         `json:"blobs,omitempty"`
	PluginSchemas []string         `json:"plugin_schemas,omitempty"`
	SecretsCheck  string           `json:"secrets_check,omitempty"`
}

type Options struct {
	Dir         string
	DatabaseURL string // for pg_dump of plugin schemas
	Store       *blobstore.Store
	Box         *secrets.Box // nil skips secrets_check
	Keep        int          // backups of the type to keep; 0 doesn't prune
	Log         *slog.Logger // BackupHandler's; nil logs to slog.Default
}

// Info describes one finished backup.
type Info struct {
	Name      string    `json:"name"`
	Type      Type      `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Size      int64     `json:"size"` // folder plus the shared blobs it lists
}

// Write writes a backup of type t to <Dir>/<t>/<t>-<time>/, then prunes that
// type to Keep. A failed write leaves a .part folder, which the next write of
// that type removes.
func Write(ctx context.Context, pool *pgxpool.Pool, t Type, o Options) (Info, error) {
	if _, ok := tables[t]; !ok {
		return Info{}, fmt.Errorf("unknown backup type %q", t)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return Info{}, err
	}
	defer conn.Release()
	// Keeps a manual and a scheduled run of one type from overlapping, and
	// prune from racing a write. Taken before the snapshot, so a run that
	// waited still sees the latest data.
	lock := `hashtext('innkeeper-backup-' || $1)`
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(`+lock+`)`, string(t)); err != nil {
		return Info{}, err
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(`+lock+`)`, string(t))

	typeDir := filepath.Join(o.Dir, string(t))
	if err := os.MkdirAll(typeDir, 0o700); err != nil {
		return Info{}, err
	}
	if err := removeParts(typeDir); err != nil {
		return Info{}, err
	}
	m := Manifest{Format: 1, Type: t, CreatedAt: time.Now().UTC(), Tables: map[string]int64{}}
	name := string(t) + "-" + m.CreatedAt.Format(timeFormat)
	final := filepath.Join(typeDir, name)
	part := final + ".part"
	if err := os.Mkdir(part, 0o700); err != nil {
		return Info{}, err
	}
	if err := os.Mkdir(filepath.Join(part, "tables"), 0o700); err != nil {
		return Info{}, err
	}
	if err := snapshot(ctx, conn, t, o, part, &m); err != nil {
		return Info{}, err
	}
	// After the snapshot ends: revisions are immutable, so listed blobs stay.
	for _, sha := range m.Blobs {
		src, err := o.Store.Path(sha)
		if err != nil {
			return Info{}, err
		}
		dst := blobPath(o.Dir, sha)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := copyFile(src, dst); err != nil {
			return Info{}, err
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Info{}, err
	}
	if err := writeFile(filepath.Join(part, "manifest.json"), func(f *os.File) error {
		_, err := f.Write(b)
		return err
	}); err != nil {
		return Info{}, err
	}
	if err := os.Rename(part, final); err != nil {
		return Info{}, err
	}
	size, err := backupSize(o.Dir, final, m)
	info := Info{Name: name, Type: t, CreatedAt: m.CreatedAt, Size: size}
	if err == nil && o.Keep > 0 {
		if err = prune(o.Dir, t, o.Keep); err != nil {
			err = fmt.Errorf("prune: %w", err)
		}
	}
	return info, err
}

// snapshot fills part and m from one read-only snapshot: the tables, the
// blob list, and the plugin schemas.
func snapshot(ctx context.Context, conn *pgxpool.Conn, t Type, o Options, part string, m *Manifest) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `SELECT max(name) FROM schema_migration`).Scan(&m.Schema); err != nil {
		return err
	}
	for _, table := range tables[t] {
		where := ""
		if table == "blob" {
			where = " WHERE " + referencedBlobs
		}
		if m.Tables[table], err = copyTable(ctx, tx, table, where, filepath.Join(part, "tables", table+".copy")); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
	}
	switch t {
	case Assets:
		rows, _ := tx.Query(ctx, `SELECT sha256 FROM blob WHERE `+referencedBlobs+` ORDER BY sha256`)
		m.Blobs, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	case Config:
		// Only names restore accepts; a plugin's schema always matches.
		rows, _ := tx.Query(ctx, `SELECT nspname::text FROM pg_namespace WHERE nspname ~ $1 ORDER BY 1`, schemaPattern.String())
		if m.PluginSchemas, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return err
		}
		if len(m.PluginSchemas) > 0 {
			if err := dumpPlugins(ctx, tx, o.DatabaseURL, m.PluginSchemas, filepath.Join(part, "plugins")); err != nil {
				return err
			}
		}
		if o.Box != nil {
			m.SecretsCheck = o.Box.Derive("backup-check")
		}
	}
	return nil
}

// removeParts removes unfinished backups and interrupted prunes in dir.
func removeParts(dir string) error {
	parts, err := filepath.Glob(filepath.Join(dir, "*.part"))
	if err != nil {
		return err
	}
	for _, p := range parts {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}

// Columns returns table's stored columns in order. Generated columns are left
// out, since COPY FROM can't write them.
func Columns(ctx context.Context, tx pgx.Tx, table string) ([]string, error) {
	rows, _ := tx.Query(ctx, `SELECT attname::text FROM pg_attribute
		WHERE attrelid = ('public.' || quote_ident($1))::regclass AND attnum > 0 AND NOT attisdropped AND attgenerated = ''
		ORDER BY attnum`, table)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// copyTable writes table's rows in COPY text format to path and returns the
// row count.
func copyTable(ctx context.Context, tx pgx.Tx, table, where, path string) (int64, error) {
	cols, err := Columns(ctx, tx, table)
	if err != nil {
		return 0, err
	}
	for i, c := range cols {
		cols[i] = pgx.Identifier{c}.Sanitize()
	}
	sql := fmt.Sprintf("COPY (SELECT %s FROM %s%s) TO STDOUT", strings.Join(cols, ", "), pgx.Identifier{"public", table}.Sanitize(), where)
	var n int64
	err = writeFile(path, func(f *os.File) error {
		tag, err := tx.Conn().PgConn().CopyTo(ctx, f, sql)
		n = tag.RowsAffected()
		return err
	})
	return n, err
}

// dumpPlugins runs pg_dump on each plugin schema in tx's snapshot, so they
// match the core tables. Each schema gets its own file, which restore runs
// as that plugin's role.
func dumpPlugins(ctx context.Context, tx pgx.Tx, dbURL string, schemas []string, dir string) error {
	var snapshot string
	if err := tx.QueryRow(ctx, `SELECT pg_export_snapshot()`).Scan(&snapshot); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	for _, s := range schemas {
		// Quoted, since pg_dump reads --schema as a pattern.
		args := []string{"--format=plain", "--inserts", "--rows-per-insert=1000", "--no-owner", "--no-privileges",
			"--snapshot=" + snapshot, `--schema="` + s + `"`}
		if err := writeFile(filepath.Join(dir, s+".sql"), func(f *os.File) error {
			cmd, err := pgDumpCommand(ctx, dbURL, f, args...)
			if err != nil {
				return err
			}
			return runPgDump(cmd)
		}); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

// writeFile creates path, has write fill it, and syncs it, so a finished
// backup never holds a file the disk lost.
func writeFile(path string, write func(*os.File) error) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	err = write(f)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func blobPath(dir, sha string) string {
	return filepath.Join(dir, string(Assets), "blobs", sha[0:2], sha[2:4], sha)
}

// copyFile writes to a temporary name first, so an interrupted copy never
// looks finished to the next run.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	part := dst + ".part"
	out, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(part, dst)
	}
	if err != nil {
		os.Remove(part)
	}
	return err
}
