package targets

import (
	"cmp"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/db"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/secrets"
)

// TestReconcileLive reconciles a real Lumiverse against a copy of a real
// library, and prints the totals and the closest likely matches. It only
// reads the app: its instance is new, so it never auto-binds.
//
// INNKEEPER_LIVE_DATABASE_URL is copied with CREATE DATABASE ... TEMPLATE,
// which refuses a database anything else is connected to; point it at a
// restored backup. INNKEEPER_PLUGINS_DIR must hold the lumiverse plugin.
func TestReconcileLive(t *testing.T) {
	env := map[string]string{}
	for _, k := range []string{"INNKEEPER_LIVE_LUMIVERSE_URL", "INNKEEPER_LIVE_LUMIVERSE_USERNAME", "INNKEEPER_LIVE_LUMIVERSE_PASSWORD",
		"INNKEEPER_LIVE_DATABASE_URL", "INNKEEPER_PLUGINS_DIR"} {
		if env[k] = os.Getenv(k); env[k] == "" {
			t.Skipf("%s is not set", k)
		}
	}
	ctx := context.Background()
	pool := liveCopy(t, env["INNKEEPER_LIVE_DATABASE_URL"])
	manifests, err := plugins.LoadDir(env["INNKEEPER_PLUGINS_DIR"], quiet)
	if err != nil {
		t.Fatal(err)
	}
	if manifests["lumiverse"] == nil {
		t.Fatalf("no lumiverse plugin in %s", env["INNKEEPER_PLUGINS_DIR"])
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	lib := library.New(pool, store)
	reg := plugins.New(pool, manifests, store, box)
	mgr := pluginhost.NewManager(noCore, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	inst, err := reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "lumiverse", Name: fmt.Sprintf("Live check %x", rand.Uint32()),
		Config:  map[string]any{"url": env["INNKEEPER_LIVE_LUMIVERSE_URL"], "username": env["INNKEEPER_LIVE_LUMIVERSE_USERNAME"]},
		Secrets: map[string]string{"password": env["INNKEEPER_LIVE_LUMIVERSE_PASSWORD"]}})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	snap, err := New(pool, lib, reg, mgr, quiet).Reconcile(ctx, ReconcileParams{InstanceID: inst, Apply: ApplyNone}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("reconciled %d characters and %d unlinked world books in %s", len(snap.Items), len(snap.Lorebooks), time.Since(start).Round(time.Second))
	t.Logf("totals: %v; library cards never pushed here: %d", snap.Totals, len(snap.Unpushed))
	var likely []ReconcileItem
	for _, it := range snap.Items {
		if it.Verdict == "likely" && len(it.Candidates) > 0 {
			likely = append(likely, it)
		}
	}
	slices.SortFunc(likely, func(a, b ReconcileItem) int {
		return cmp.Compare(a.Candidates[0].DifferingFields, b.Candidates[0].DifferingFields)
	})
	for _, it := range likely[:min(10, len(likely))] {
		c := it.Candidates[0]
		t.Logf("likely: %s (%s) ~ card %d %s: %d fields differ, %d conflicts, reasons %v",
			it.Name, it.RemoteID, c.CardID, c.Name, c.DifferingFields, c.Conflicts, c.Reasons)
	}
}

// liveCopy copies the database at url into a new one, migrated, and drops
// the copy when the test ends.
func liveCopy(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	var source string
	err = admin.QueryRow(ctx, "SELECT current_database()").Scan(&source)
	// TEMPLATE needs the source to have no other connections, this one included.
	admin.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = "postgres"
	other, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close(ctx) })
	name := fmt.Sprintf("innkeeper_live_%x", rand.Uint64())
	if _, err := other.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, pgx.Identifier{source}.Sanitize())); err != nil {
		t.Fatalf("copy %s: %v", source, err)
	}
	t.Cleanup(func() {
		if _, err := other.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	})
	pcfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	pcfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}
