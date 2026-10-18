package plugins

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

// newDBRegistry returns a registry with an opted-in "cat-log" plugin and a
// plain one, plus the test database's URL. Roles made in the database are
// dropped when the test ends, since they outlive it.
func newDBRegistry(t *testing.T) (*Registry, *pgxpool.Pool, string) {
	t.Helper()
	pool := testdb.New(t)
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	manifests := map[string]*protocol.Manifest{
		"cat-log": {Name: "cat-log", Version: "0.0.1", Database: true},
		"plain":   {Name: "plain", Version: "0.0.1"},
	}
	reg := New(pool, manifests, &blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS plugin_cat_log CASCADE`); err != nil {
			t.Error(err)
		}
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
	return reg, pool, u.String()
}

func TestProvisionDatabases(t *testing.T) {
	ctx := context.Background()
	reg, pool, dbURL := newDBRegistry(t)
	if err := reg.ProvisionDatabases(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "cat-log", Name: new("Catalog")})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, inst.Params.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `CREATE TABLE entry (n int); INSERT INTO entry VALUES (7)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT n FROM plugin_cat_log.entry`).Scan(&n); err != nil || n != 7 {
		t.Fatalf("read own table: %d, %v", n, err)
	}
	if _, err := conn.Exec(ctx, `SELECT FROM public.card`); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("read card: %v, want permission denied", err)
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE public.sneaky (n int)`); err == nil {
		t.Error("created a table in public")
	}

	// A second run is fine, and takes back a schema, and what's in it, given
	// to someone else, as after a restore into a database with another name.
	other := pool.Config().ConnConfig.Database + "_other"
	if _, err := pool.Exec(ctx, `CREATE ROLE `+other+`; ALTER SCHEMA plugin_cat_log OWNER TO `+other+`;
		SET ROLE `+other+`; SET search_path = plugin_cat_log;
		CREATE TABLE restored (id serial PRIMARY KEY); CREATE SEQUENCE loose; CREATE VIEW seen AS SELECT 1;
		CREATE FUNCTION one() RETURNS int LANGUAGE sql AS 'SELECT 1'; CREATE TYPE mood AS ENUM ('ok');
		CREATE DOMAIN pos AS int; CREATE TYPE pair AS (a int); CREATE TYPE span AS RANGE (subtype = int);
		RESET ROLE; RESET search_path`); err != nil {
		t.Fatal(err)
	}
	if err := reg.ProvisionDatabases(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := pool.QueryRow(ctx, `SELECT nspowner::regrole::text FROM pg_namespace WHERE nspname = 'plugin_cat_log'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if want := pool.Config().ConnConfig.Database + "_plugin_cat_log"; owner != want {
		t.Errorf("schema owner %s, want %s", owner, want)
	}
	var strays []string
	rows, err := pool.Query(ctx, `
		SELECT relname FROM pg_class WHERE relnamespace = 'plugin_cat_log'::regnamespace AND relowner <> $1::regrole
		UNION ALL SELECT proname FROM pg_proc WHERE pronamespace = 'plugin_cat_log'::regnamespace AND proowner <> $1::regrole
		UNION ALL SELECT typname FROM pg_type WHERE typnamespace = 'plugin_cat_log'::regnamespace AND typowner <> $1::regrole`, owner)
	if err == nil {
		strays, err = pgx.CollectRows(rows, pgx.RowTo[string])
	}
	if err != nil || len(strays) > 0 {
		t.Errorf("not given back: %v, %v", strays, err)
	}
	if _, err := conn.Exec(ctx, `ALTER TABLE restored ADD COLUMN note text`); err != nil {
		t.Errorf("alter a restored table: %v", err)
	}
}

func TestProvisionDatabasesOptIn(t *testing.T) {
	ctx := context.Background()
	reg, pool, dbURL := newDBRegistry(t)
	if err := reg.ProvisionDatabases(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "plain", Name: new("Plain")})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Params.DatabaseURL != "" {
		t.Errorf("plain plugin got database_url %q", inst.Params.DatabaseURL)
	}
	var roles int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_roles WHERE rolname = current_database() || '_plugin_plain'`).Scan(&roles); err != nil || roles != 0 {
		t.Errorf("plain plugin roles: %d, %v", roles, err)
	}
}

func TestProvisionDatabasesKeyValueURL(t *testing.T) {
	ctx := context.Background()
	reg, _, _ := newDBRegistry(t)
	if err := reg.ProvisionDatabases(ctx, "host=localhost dbname=innkeeper"); err == nil || !strings.Contains(err.Error(), "postgres://") {
		t.Errorf("key-value URL with an opted-in plugin: %v", err)
	}
	delete(reg.manifests, "cat-log")
	if err := reg.ProvisionDatabases(ctx, "host=localhost dbname=innkeeper"); err != nil {
		t.Errorf("key-value URL without opted-in plugins: %v", err)
	}
}

func TestPluginUsesItsDatabase(t *testing.T) {
	ctx := context.Background()
	reg, _, dbURL := newDBRegistry(t)
	m, err := protocol.LoadManifest(testutil.FakePlugin(t, "-mode=database"))
	if err != nil {
		t.Fatal(err)
	}
	reg.manifests["fake"] = m
	if err := reg.ProvisionDatabases(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake")})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	mgr := pluginhost.NewManager(nil, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	var got string
	if err := reg.Call(ctx, mgr, inst, "db", "meow", &got); err != nil || got != "meow" {
		t.Fatalf("db call: %q, %v", got, err)
	}
}

func TestRemoveData(t *testing.T) {
	ctx := context.Background()
	reg, pool, dbURL := newDBRegistry(t)
	if err := reg.ProvisionDatabases(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "cat-log", Name: new("Cats")}); err != nil {
		t.Fatal(err)
	}
	if err := reg.RemoveData(ctx, "cat-log"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("remove data while installed: %v", err)
	}
	delete(reg.manifests, "cat-log")
	if err := reg.RemoveData(ctx, "cat-log"); err != nil {
		t.Fatal(err)
	}
	var instances int
	var schema, role bool
	err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM plugin_instance WHERE plugin = 'cat-log'),
		EXISTS (SELECT FROM pg_namespace WHERE nspname = 'plugin_cat_log'),
		EXISTS (SELECT FROM pg_roles WHERE rolname = current_database() || '_plugin_cat_log')`).Scan(&instances, &schema, &role)
	if err != nil {
		t.Fatal(err)
	}
	if instances != 0 || schema || role {
		t.Fatalf("left instances %d, schema %v, role %v", instances, schema, role)
	}
}

func TestRemoveDataOfAnImporterThatImported(t *testing.T) {
	ctx := context.Background()
	reg, pool, dbURL := newDBRegistry(t)
	if err := reg.ProvisionDatabases(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "cat-log", Name: new("Cats")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `WITH c AS (INSERT INTO card (slug, provenance) VALUES ('a', 'cat-log:a') RETURNING id)
		INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data, accepted_hash, push, imported)
		SELECT $1, id, 'cat-log:a', 'unchanged', '{}', '', false, true FROM c`, id); err != nil {
		t.Fatal(err)
	}
	delete(reg.manifests, "cat-log")
	if err := reg.RemoveData(ctx, "cat-log"); err != nil {
		t.Fatalf("remove data: %v", err)
	}
}
