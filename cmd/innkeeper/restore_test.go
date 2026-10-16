package main

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/testdb"
)

// pluginDB returns a fresh database and its URL. The plugin roles made in it
// outlive it, so they're dropped when the test ends.
func pluginDB(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	pool := testdb.New(t)
	t.Cleanup(func() {
		ctx := context.Background()
		rows, err := pool.Query(ctx, `SELECT rolname FROM pg_roles WHERE rolname LIKE current_database() || '\_%'`)
		if err != nil {
			t.Error(err)
			return
		}
		roles, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Error(err)
		}
		for _, role := range roles {
			r := pgx.Identifier{role}.Sanitize()
			if _, err := pool.Exec(ctx, `DROP OWNED BY `+r+`; DROP ROLE `+r); err != nil {
				t.Errorf("drop role %s: %v", role, err)
			}
		}
	})
	u, err := url.Parse(os.Getenv(testdb.EnvVar))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + pool.Config().ConnConfig.Database
	return pool, u.String()
}

func TestRestoreConfigWithPluginDatabase(t *testing.T) {
	if _, err := exec.LookPath("pg_dump"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	ctx := context.Background()
	t.Setenv("INNKEEPER_SECRETS_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("INNKEEPER_BUILTIN_PLUGINS_DIR", "testdata/plugins")
	t.Setenv("INNKEEPER_PLUGINS_DIR", t.TempDir())
	t.Setenv("INNKEEPER_BLOB_DIR", t.TempDir())

	_, srcURL := pluginDB(t)
	t.Setenv("INNKEEPER_DATABASE_URL", srcURL)
	svc, err := openServices(ctx, slog.New(slog.DiscardHandler), 0)
	if err != nil {
		t.Fatal(err)
	}
	id, err := svc.registry.CreateInstance(ctx, plugins.NewInstance{Plugin: "notes", Name: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := svc.registry.Instance(ctx, id)
	svc.close()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, inst.Params.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `CREATE TABLE note (body text); INSERT INTO note VALUES ('remember the forge')`)
	conn.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := backupCmd([]string{dir, "config"}); err != nil {
		t.Fatal(err)
	}
	folders, err := filepath.Glob(filepath.Join(dir, "config", "config-*"))
	if err != nil || len(folders) != 1 {
		t.Fatalf("backups %v, %v", folders, err)
	}

	dst, dstURL := pluginDB(t)
	t.Setenv("INNKEEPER_DATABASE_URL", dstURL)
	t.Setenv("INNKEEPER_BLOB_DIR", t.TempDir())
	if err := restoreCmd(folders); err != nil {
		t.Fatal(err)
	}
	var body, owner string
	var instances int
	if err := dst.QueryRow(ctx, `SELECT (SELECT body FROM plugin_notes.note),
		(SELECT nspowner::regrole::text FROM pg_namespace WHERE nspname = 'plugin_notes'),
		(SELECT count(*) FROM plugin_instance WHERE plugin = 'notes')`).Scan(&body, &owner, &instances); err != nil {
		t.Fatal(err)
	}
	if want := dst.Config().ConnConfig.Database + "_plugin_notes"; body != "remember the forge" || owner != want || instances != 1 {
		t.Fatalf("note %q, schema owner %s (want %s), %d instances", body, owner, want, instances)
	}
}
