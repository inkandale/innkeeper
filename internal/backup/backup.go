// Package backup writes, lists, prunes and restores scheduled backups of the
// asset store and the app configuration.
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

const defaultKeep = 7

// Settings is the backup settings from the environment.
type Settings struct {
	Dir   string
	Every map[Type]time.Duration // only scheduled types
	Keep  map[Type]int           // every type with a schedule or a KEEP
}

// SettingsFromEnv reads INNKEEPER_BACKUP_DIR and the per-type _EVERY and _KEEP
// variables. Errors name the variable at fault.
func SettingsFromEnv() (Settings, error) {
	c := Settings{
		Dir:   os.Getenv("INNKEEPER_BACKUP_DIR"),
		Every: map[Type]time.Duration{},
		Keep:  map[Type]int{},
	}
	for _, t := range Types {
		name := "INNKEEPER_BACKUP_" + strings.ToUpper(string(t))
		if v := os.Getenv(name + "_EVERY"); v != "" {
			d, err := parseEvery(v)
			if err != nil {
				return Settings{}, fmt.Errorf("%s_EVERY: %w", name, err)
			}
			if c.Dir == "" {
				return Settings{}, fmt.Errorf("%s_EVERY is set but INNKEEPER_BACKUP_DIR is not", name)
			}
			c.Every[t] = d
			c.Keep[t] = defaultKeep
		}
		if v := os.Getenv(name + "_KEEP"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Settings{}, fmt.Errorf("%s_KEEP: %q is not an integer of at least 1", name, v)
			}
			c.Keep[t] = n
		}
	}
	return c, nil
}

// parseEvery accepts a Go duration or whole days ("7d"), which
// time.ParseDuration lacks.
func parseEvery(s string) (time.Duration, error) {
	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("%q is not a duration like 24h or 7d", s)
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("%q is not a duration like 24h or 7d", s)
		}
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q must be positive", s)
	}
	return d, nil
}

// tables lists each type's tables in load order. Jobs, tokens, sessions and
// OAuth grants are never backed up.
var tables = map[Type][]string{
	Assets: {"blob", "card", "card_revision", "lorebook", "lorebook_revision", "card_lorebook",
		"tag", "card_tag", "collection", "collection_card", "card_source", "lorebook_source"},
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
}

// Info describes one finished backup.
type Info struct {
	Name      string    `json:"name"`
	Type      Type      `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Size      int64     `json:"size"` // folder plus the shared blobs it lists
}

// Write writes a backup of type t to <Dir>/<t>/<t>-<time>/. A failed write
// leaves a .part folder, which the next write of that type removes.
func Write(ctx context.Context, pool *pgxpool.Pool, t Type, o Options) (Info, error) {
	if _, ok := tables[t]; !ok {
		return Info{}, fmt.Errorf("unknown backup type %q", t)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Info{}, err
	}
	defer tx.Rollback(ctx)
	// Keeps a manual and a scheduled run of one type from overlapping.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('innkeeper-backup-' || $1))`, string(t)); err != nil {
		return Info{}, err
	}
	typeDir := filepath.Join(o.Dir, string(t))
	if err := os.MkdirAll(typeDir, 0o700); err != nil {
		return Info{}, err
	}
	parts, err := filepath.Glob(filepath.Join(typeDir, "*.part"))
	if err != nil {
		return Info{}, err
	}
	for _, p := range parts {
		if err := os.RemoveAll(p); err != nil {
			return Info{}, err
		}
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
	if err := tx.QueryRow(ctx, `SELECT max(name) FROM schema_migration`).Scan(&m.Schema); err != nil {
		return Info{}, err
	}
	for _, table := range tables[t] {
		where := ""
		if table == "blob" {
			where = " WHERE " + referencedBlobs
		}
		if m.Tables[table], err = copyTable(ctx, tx, table, where, filepath.Join(part, "tables", table+".copy")); err != nil {
			return Info{}, fmt.Errorf("%s: %w", table, err)
		}
	}

	switch t {
	case Assets:
		rows, _ := tx.Query(ctx, `SELECT sha256 FROM blob WHERE `+referencedBlobs+` ORDER BY sha256`)
		if m.Blobs, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return Info{}, err
		}
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
	case Config:
		rows, _ := tx.Query(ctx, `SELECT nspname::text FROM pg_namespace WHERE nspname LIKE 'plugin\_%' ORDER BY 1`)
		if m.PluginSchemas, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return Info{}, err
		}
		if len(m.PluginSchemas) > 0 {
			if err := dumpPlugins(ctx, tx, o.DatabaseURL, m.PluginSchemas, filepath.Join(part, "plugins.sql")); err != nil {
				return Info{}, err
			}
		}
		if o.Box != nil {
			m.SecretsCheck = o.Box.Derive("backup-check")
		}
	}

	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Info{}, err
	}
	if err := os.WriteFile(filepath.Join(part, "manifest.json"), b, 0o600); err != nil {
		return Info{}, err
	}
	if err := os.Rename(part, final); err != nil {
		return Info{}, err
	}
	size, err := backupSize(o.Dir, final, m)
	return Info{Name: name, Type: t, CreatedAt: m.CreatedAt, Size: size}, err
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
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	sql := fmt.Sprintf("COPY (SELECT %s FROM %s%s) TO STDOUT", strings.Join(cols, ", "), pgx.Identifier{table}.Sanitize(), where)
	tag, err := tx.Conn().PgConn().CopyTo(ctx, f, sql)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return tag.RowsAffected(), err
}

// dumpPlugins runs pg_dump on the plugin schemas in tx's snapshot, so they
// match the core tables.
func dumpPlugins(ctx context.Context, tx pgx.Tx, dbURL string, schemas []string, path string) error {
	var snapshot string
	if err := tx.QueryRow(ctx, `SELECT pg_export_snapshot()`).Scan(&snapshot); err != nil {
		return err
	}
	args := []string{"--format=plain", "--inserts", "--rows-per-insert=1000", "--no-owner", "--no-privileges", "--snapshot=" + snapshot}
	for _, s := range schemas {
		args = append(args, "--schema="+s)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd, err := pgDumpCommand(dbURL, f, args...)
	if err != nil {
		return err
	}
	if err := runPgDump(cmd); err != nil {
		return err
	}
	return f.Close()
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

func readManifest(folder string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(folder, "manifest.json"))
	if err == nil {
		err = json.Unmarshal(b, &m)
	}
	return m, err
}

// backupSize adds up folder's files and the shared blobs m lists.
func backupSize(dir, folder string, m Manifest) (int64, error) {
	var size int64
	err := filepath.WalkDir(folder, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		size += fi.Size()
		return err
	})
	for _, sha := range m.Blobs {
		if fi, serr := os.Stat(blobPath(dir, sha)); serr == nil {
			size += fi.Size()
		}
	}
	return size, err
}

// backups returns the finished backups of type t, newest first, with their
// manifests.
func backups(dir string, t Type) ([]Info, []Manifest, error) {
	es, err := os.ReadDir(filepath.Join(dir, string(t)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var infos []Info
	var ms []Manifest
	for _, e := range es {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, string(t)+"-") || strings.HasSuffix(name, ".part") {
			continue
		}
		folder := filepath.Join(dir, string(t), name)
		m, err := readManifest(folder)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		size, err := backupSize(dir, folder, m)
		if err != nil {
			return nil, nil, err
		}
		infos = append(infos, Info{Name: name, Type: t, CreatedAt: m.CreatedAt, Size: size})
		ms = append(ms, m)
	}
	// Folder names sort by time.
	slices.Reverse(infos)
	slices.Reverse(ms)
	return infos, ms, nil
}

// List returns every finished backup, newest first.
func List(dir string) ([]Info, error) {
	var all []Info
	for _, t := range Types {
		infos, _, err := backups(dir, t)
		if err != nil {
			return nil, err
		}
		all = append(all, infos...)
	}
	slices.SortFunc(all, func(a, b Info) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return all, nil
}

// Prune removes the backups of type t past the newest keep and returns how
// many it removed. For assets it then removes shared blobs no remaining
// backup lists.
func Prune(dir string, t Type, keep int) (int, error) {
	infos, ms, err := backups(dir, t)
	if err != nil {
		return 0, err
	}
	n := 0
	listed := map[string]bool{}
	for i := range infos {
		if i < keep {
			for _, sha := range ms[i].Blobs {
				listed[sha] = true
			}
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, string(t), infos[i].Name)); err != nil {
			return n, err
		}
		n++
	}
	if t != Assets {
		return n, nil
	}
	// ponytail: an assets Write running now could lose blobs it copied or
	// reused; callers run Prune after Write, never beside it. Take the backup
	// lock here if that changes.
	blobs := filepath.Join(dir, string(Assets), "blobs")
	err = filepath.WalkDir(blobs, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == blobs {
			return fs.SkipAll
		}
		if err != nil || d.IsDir() || listed[d.Name()] {
			return err
		}
		return os.Remove(path)
	})
	return n, err
}
