package backup

import (
	"bufio"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/secrets"
	"github.com/davidgibbons/innkeeper/internal/testdb"
)

// setup returns a test database, its blob store, and Options writing to a
// new folder.
func setup(t *testing.T) (*pgxpool.Pool, *library.Library, Options) {
	t.Helper()
	pool := testdb.New(t)
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	u, err := url.Parse(os.Getenv(testdb.EnvVar))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + pool.Config().ConnConfig.Database
	return pool, library.New(pool, store), Options{Dir: t.TempDir(), DatabaseURL: u.String(), Store: store}
}

// seed adds a card with an avatar, a lorebook, a tag, a collection, and a
// blob no revision uses. It returns the avatar's and the unused blob's hashes.
func seed(t *testing.T, lib *library.Library) (avatar, unused string) {
	t.Helper()
	ctx := context.Background()
	a, err := lib.PutBlob(ctx, strings.NewReader("avatar"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := lib.PutBlob(ctx, strings.NewReader("unused"))
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := lib.Cards.Create(ctx, "brakka", "upload",
		library.Edit{Data: json.RawMessage(`{"name": "Brakka"}`), AvatarBlob: a.SHA256, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := lib.Lorebooks.Create(ctx, "lore", "upload", library.Edit{Data: json.RawMessage(`{"name": "Lore", "entries": []}`), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.LinkLorebook(ctx, c.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.SetCardTags(ctx, c.ID, []string{"dwarf"}); err != nil {
		t.Fatal(err)
	}
	col, err := lib.CreateCollection(ctx, "Favorites", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddToCollection(ctx, col.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	return a.SHA256, u.SHA256
}

func mustManifest(t *testing.T, folder string) Manifest {
	t.Helper()
	m, err := readManifest(folder)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func countLines(t *testing.T, path string) int64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var n int64
	for s := bufio.NewScanner(f); s.Scan(); {
		n++
	}
	return n
}

func blobFile(dir, sha string) string {
	return filepath.Join(dir, "assets", "blobs", sha[:2], sha[2:4], sha)
}

func TestWriteAssets(t *testing.T) {
	pool, lib, o := setup(t)
	ctx := context.Background()
	avatar, unused := seed(t, lib)

	info, err := Write(ctx, pool, Assets, o)
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(o.Dir, "assets", info.Name)
	m := mustManifest(t, folder)
	var schema string
	if err := pool.QueryRow(ctx, `SELECT max(name) FROM schema_migration`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if m.Format != 1 || m.Type != Assets || m.Schema != schema || m.SecretsCheck != "" || len(m.PluginSchemas) != 0 {
		t.Fatalf("manifest = %+v", m)
	}
	if !slices.Equal(m.Blobs, []string{avatar}) {
		t.Fatalf("blobs = %v, want only the avatar", m.Blobs)
	}
	want := map[string]int64{
		"blob": 1, "card": 1, "card_revision": 1, "lorebook": 1, "lorebook_revision": 1, "card_lorebook": 1,
		"tag": 1, "card_tag": 1, "collection": 1, "collection_card": 1, "card_source": 0, "lorebook_source": 0,
	}
	if len(m.Tables) != len(want) {
		t.Fatalf("tables = %v", m.Tables)
	}
	for table, n := range want {
		if m.Tables[table] != n {
			t.Errorf("%s: manifest says %d rows, want %d", table, m.Tables[table], n)
		}
		if got := countLines(t, filepath.Join(folder, "tables", table+".copy")); got != n {
			t.Errorf("%s.copy has %d lines, want %d", table, got, n)
		}
	}
	if b, err := os.ReadFile(blobFile(o.Dir, avatar)); err != nil || string(b) != "avatar" {
		t.Fatalf("avatar copy: %q, %v", b, err)
	}
	if _, err := os.Stat(blobFile(o.Dir, unused)); err == nil {
		t.Fatal("copied an unreferenced blob")
	}
	for p, mode := range map[string]os.FileMode{folder: 0o700, filepath.Join(folder, "manifest.json"): 0o600, blobFile(o.Dir, avatar): 0o600} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != mode {
			t.Errorf("%s: %v, %v; want %v", p, fi, err, mode)
		}
	}

	// A second backup reuses the copied blob.
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(blobFile(o.Dir, avatar), old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(ctx, pool, Assets, o); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(blobFile(o.Dir, avatar)); err != nil || !fi.ModTime().Equal(old) {
		t.Fatalf("second backup rewrote the blob: %v, %v", fi, err)
	}
}

func TestWriteConfig(t *testing.T) {
	pool, lib, o := setup(t)
	ctx := context.Background()
	seed(t, lib)
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	o.Box = box
	if _, err := pool.Exec(ctx, `
		INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'fake');
		INSERT INTO setting (key, value) VALUES ('k', 'v');
		INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data, accepted_hash)
			SELECT i.id, c.id, 'r1', 'unchanged', '{}', 'h' FROM plugin_instance i, card c;
		INSERT INTO push (binding_id, revision_id, pushed_hash, remote_id)
			SELECT id, 1, 'h', 'r1' FROM binding`); err != nil {
		t.Fatal(err)
	}

	info, err := Write(ctx, pool, Config, o)
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(o.Dir, "config", info.Name)
	m := mustManifest(t, folder)
	if m.Type != Config || len(m.Blobs) != 0 || m.SecretsCheck != box.Derive("backup-check") || len(m.PluginSchemas) != 0 {
		t.Fatalf("manifest = %+v", m)
	}
	for _, table := range []string{"plugin_instance", "setting", "binding", "push"} {
		if m.Tables[table] != 1 || countLines(t, filepath.Join(folder, "tables", table+".copy")) != 1 {
			t.Errorf("%s: %d rows", table, m.Tables[table])
		}
	}
	if _, err := os.Stat(filepath.Join(folder, "plugins.sql")); err == nil {
		t.Fatal("plugins.sql without plugin schemas")
	}
	if _, err := os.Stat(filepath.Join(o.Dir, "assets")); err == nil {
		t.Fatal("config backup touched assets")
	}
}

func TestWriteConfigPluginSchemas(t *testing.T) {
	if _, err := exec.LookPath("pg_dump"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	pool, _, o := setup(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE SCHEMA plugin_fake;
		CREATE TABLE plugin_fake.note (body text);
		INSERT INTO plugin_fake.note VALUES ('remember the forge')`); err != nil {
		t.Fatal(err)
	}
	info, err := Write(ctx, pool, Config, o)
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(o.Dir, "config", info.Name)
	if m := mustManifest(t, folder); !slices.Equal(m.PluginSchemas, []string{"plugin_fake"}) || m.SecretsCheck != "" {
		t.Fatalf("manifest = %+v", m)
	}
	b, err := os.ReadFile(filepath.Join(folder, "plugins.sql"))
	if err != nil || !strings.Contains(string(b), "remember the forge") {
		t.Fatalf("plugins.sql: %v\n%s", err, b)
	}
}

func TestWriteFailureLeavesPartForNextRun(t *testing.T) {
	pool, _, o := setup(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE SCHEMA plugin_fake`); err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	if _, err := Write(ctx, pool, Config, o); err == nil {
		t.Fatal("want error without pg_dump")
	}
	es, _ := os.ReadDir(filepath.Join(o.Dir, "config"))
	if len(es) != 1 || !strings.HasSuffix(es[0].Name(), ".part") {
		t.Fatalf("left %v, want one .part folder", es)
	}

	t.Setenv("PATH", path)
	if _, err := pool.Exec(ctx, `DROP SCHEMA plugin_fake`); err != nil {
		t.Fatal(err)
	}
	info, err := Write(ctx, pool, Config, o)
	if err != nil {
		t.Fatal(err)
	}
	es, _ = os.ReadDir(filepath.Join(o.Dir, "config"))
	if len(es) != 1 || es[0].Name() != info.Name {
		t.Fatalf("left %v, want only %s", es, info.Name)
	}
}

func TestListAndPrune(t *testing.T) {
	pool, lib, o := setup(t)
	ctx := context.Background()
	avatar, _ := seed(t, lib)
	older, err := Write(ctx, pool, Assets, o)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Write(ctx, pool, Config, o)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := Write(ctx, pool, Assets, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(o.Dir, "assets", "assets-20000101T000000.000000Z.part"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A folder without a manifest is skipped, not an error.
	if err := os.Mkdir(filepath.Join(o.Dir, "assets", "assets-20000101T000000.000000Z"), 0o700); err != nil {
		t.Fatal(err)
	}

	// Make the older backup list a blob the newer one doesn't.
	gone := strings.Repeat("e", 64)
	if err := os.MkdirAll(filepath.Dir(blobFile(o.Dir, gone)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobFile(o.Dir, gone), []byte("gone"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldFolder := filepath.Join(o.Dir, "assets", older.Name)
	m := mustManifest(t, oldFolder)
	m.Blobs = append(m.Blobs, gone)
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(oldFolder, "manifest.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	list, err := List(o.Dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, i := range list {
		names = append(names, i.Name)
	}
	if !slices.Equal(names, []string{newer.Name, cfg.Name, older.Name}) {
		t.Fatalf("List = %v", names)
	}
	if list[2].Size <= list[0].Size || list[2].Type != Assets || list[1].Type != Config {
		t.Fatalf("List = %+v; the older backup lists one more blob", list)
	}

	// It would also keep every blob; TestPruneKeepsBlobsOfDamagedBackup covers that.
	if err := os.Remove(filepath.Join(o.Dir, "assets", "assets-20000101T000000.000000Z")); err != nil {
		t.Fatal(err)
	}
	o.Keep = 2
	newest, err := Write(ctx, pool, Assets, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldFolder); err == nil {
		t.Fatal("older backup still there")
	}
	if _, err := os.Stat(blobFile(o.Dir, gone)); err == nil {
		t.Fatal("blob only the pruned backup listed is still there")
	}
	if _, err := os.Stat(blobFile(o.Dir, avatar)); err != nil {
		t.Fatalf("shared blob removed: %v", err)
	}
	list, err = List(o.Dir)
	if err != nil || len(list) != 3 || list[0].Name != newest.Name || list[1].Name != newer.Name {
		t.Fatalf("after prune: %+v, %v", list, err)
	}
}

func TestListMissingDir(t *testing.T) {
	if list, err := List(filepath.Join(t.TempDir(), "none")); err != nil || len(list) != 0 {
		t.Fatalf("List = %v, %v", list, err)
	}
}

func TestPruneKeepsBlobsOfDamagedBackup(t *testing.T) {
	pool, lib, o := setup(t)
	ctx := context.Background()
	seed(t, lib)
	damaged := filepath.Join(o.Dir, "assets", "assets-20000101T000000.000000Z")
	if err := os.MkdirAll(damaged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(damaged, "manifest.json"), []byte("{garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Only the damaged backup could list this blob.
	maybe := strings.Repeat("d", 64)
	if err := os.MkdirAll(filepath.Dir(blobFile(o.Dir, maybe)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobFile(o.Dir, maybe), []byte("maybe"), 0o600); err != nil {
		t.Fatal(err)
	}
	o.Keep = 1
	for range 2 {
		if _, err := Write(ctx, pool, Assets, o); err != nil {
			t.Fatal(err)
		}
	}
	if list, err := List(o.Dir); err != nil || len(list) != 1 {
		t.Fatalf("List = %+v, %v; want the newest backup only", list, err)
	}
	if _, err := os.Stat(blobFile(o.Dir, maybe)); err != nil {
		t.Fatalf("blob a damaged backup might list was removed: %v", err)
	}
}
