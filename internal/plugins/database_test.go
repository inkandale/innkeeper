package plugins

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/secrets"
	"github.com/davidgibbons/innkeeper/internal/testdb"
	"github.com/davidgibbons/innkeeper/protocol"
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
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "cat-log", Name: "Catalog"})
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

	// A second run is fine, and takes back a schema given to someone else.
	other := pool.Config().ConnConfig.Database + "_other"
	if _, err := pool.Exec(ctx, `CREATE ROLE `+other+`; ALTER SCHEMA plugin_cat_log OWNER TO `+other); err != nil {
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
}

func TestProvisionDatabasesOptIn(t *testing.T) {
	ctx := context.Background()
	reg, pool, dbURL := newDBRegistry(t)
	if err := reg.ProvisionDatabases(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "plain", Name: "Plain"})
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
