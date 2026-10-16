package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/secrets"
)

// needPgDump skips without pg_dump, except in CI, where it fails.
func needPgDump(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pg_dump"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
}

func testBox(t *testing.T, b byte) *secrets.Box {
	t.Helper()
	box, err := secrets.New(bytes.Repeat([]byte{b}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

// source seeds a database with assets, config, and a plugin schema, and
// returns an archive of each type.
func source(t *testing.T) (assets, config []byte, box *secrets.Box) {
	t.Helper()
	needPgDump(t)
	pool, lib, o := setup(t)
	ctx := context.Background()
	seed(t, lib)
	box = testBox(t, 1)
	o.Box = box
	sealed, err := box.Seal(map[string]string{"token": "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plugin_instance (plugin, name, secrets) VALUES ('fake', 'fake', $1)`, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO card_source (card_id, kind, ref, linked_by) SELECT id, 'chub', 'a/brakka', 'owner' FROM card;
		INSERT INTO setting (key, value) VALUES ('k', 'v');
		INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data, accepted_hash)
			SELECT i.id, c.id, 'r1', 'unchanged', '{}', 'h' FROM plugin_instance i, card c;
		INSERT INTO push (binding_id, revision_id, pushed_hash, remote_id)
			SELECT b.id, c.head_revision_id, 'h', 'r1' FROM binding b JOIN card c ON c.id = b.card_id;
		CREATE SCHEMA plugin_fake;
		CREATE TABLE plugin_fake.note (body text);
		INSERT INTO plugin_fake.note VALUES ('remember the forge')`); err != nil {
		t.Fatal(err)
	}
	return archive(t, pool, Assets, o), archive(t, pool, Config, o), box
}

func archive(t *testing.T, pool *pgxpool.Pool, typ Type, o Options) []byte {
	t.Helper()
	info, err := Write(context.Background(), pool, typ, o)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteArchive(&buf, o.Dir, info.Name); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// target is an empty database to restore into.
type target struct {
	pool  *pgxpool.Pool
	lib   *library.Library
	store *blobstore.Store
}

func newTarget(t *testing.T) target {
	t.Helper()
	pool, lib, o := setup(t)
	return target{pool, lib, o.Store}
}

// pluginRole creates plugin_fake's role the way ProvisionDatabases does.
// Roles are cluster-wide, so the name comes from the test database.
func (tg target) pluginRole(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	role := pgx.Identifier{tg.pool.Config().ConnConfig.Database + "_plugin_fake"}.Sanitize()
	if _, err := tg.pool.Exec(ctx, `CREATE ROLE `+role+` NOINHERIT; GRANT `+role+` TO CURRENT_USER`); err != nil {
		t.Fatal(err)
	}
	// Runs before testdb drops the database, so DROP OWNED reaches its objects.
	t.Cleanup(func() {
		if _, err := tg.pool.Exec(ctx, `DROP OWNED BY `+role+`; DROP ROLE `+role); err != nil {
			t.Errorf("drop role: %v", err)
		}
	})
}

// rebuild returns archive with edit applied to its entries' bodies. Entries
// edit adds go last.
func rebuild(t *testing.T, archive []byte, edit func(bodies map[string]string)) []byte {
	t.Helper()
	names, bodies := entries(t, archive)
	edit(bodies)
	var files [][2]string
	for _, n := range names {
		files = append(files, [2]string{n, bodies[n]})
		delete(bodies, n)
	}
	for _, n := range slices.Sorted(maps.Keys(bodies)) {
		files = append(files, [2]string{n, bodies[n]})
	}
	return archiveOf(t, files...)
}

// editManifest rewrites an archive's manifest.json inside rebuild.
func editManifest(t *testing.T, bodies map[string]string, edit func(*Manifest)) {
	t.Helper()
	var m Manifest
	if err := json.Unmarshal([]byte(bodies["manifest.json"]), &m); err != nil {
		t.Fatal(err)
	}
	edit(&m)
	b, _ := json.Marshal(m)
	bodies["manifest.json"] = string(b)
}

// wantDenied fails unless err is PostgreSQL refusing the plugin role.
func wantDenied(t *testing.T, what string, err error) {
	t.Helper()
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "42501" {
		t.Fatalf("%s: err = %v, want SQLSTATE 42501", what, err)
	}
}

func (tg target) restore(archive []byte, box *secrets.Box, replace bool) (Result, error) {
	return Restore(context.Background(), bytes.NewReader(archive),
		RestoreOptions{Conn: tg.pool.Config().ConnConfig, Store: tg.store, Box: box, Replace: replace})
}

func (tg target) count(t *testing.T, query string) int {
	t.Helper()
	var n int
	if err := tg.pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (tg target) slugs(t *testing.T) []string {
	t.Helper()
	rows, err := tg.pool.Query(context.Background(), `SELECT slug FROM card ORDER BY slug`)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func checkAssets(t *testing.T, tg target) {
	t.Helper()
	ctx := context.Background()
	var avatar, name string
	if err := tg.pool.QueryRow(ctx, `SELECT r.avatar_blob, r.data->>'name' FROM card c
		JOIN card_revision r ON r.id = c.head_revision_id WHERE c.slug = 'brakka'`).Scan(&avatar, &name); err != nil {
		t.Fatal(err)
	}
	if name != "Brakka" {
		t.Fatalf("head revision name = %q", name)
	}
	p, err := tg.store.Path(avatar)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(p); err != nil || string(b) != "avatar" {
		t.Fatalf("avatar file: %q, %v", b, err)
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM card_lorebook`: 1,
		`SELECT count(*) FROM collection_card JOIN collection c ON c.id = collection_id WHERE name = 'Favorites'`: 1,
		`SELECT count(*) FROM card_source WHERE ref = 'a/brakka'`:                                                 1,
	} {
		if got := tg.count(t, q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
}

func TestRestoreAssets(t *testing.T) {
	assets, _, _ := source(t)
	tg := newTarget(t)
	res, err := tg.restore(assets, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Type != Assets || res.Blobs != 1 || res.Rows["card"] != 1 || res.Rows["card_source"] != 1 {
		t.Fatalf("result = %+v", res)
	}
	checkAssets(t, tg)
	c, _, err := tg.lib.Cards.Create(context.Background(), "new", "upload",
		library.Edit{Data: json.RawMessage(`{"name": "New"}`), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if max := tg.count(t, `SELECT max(id) FROM card WHERE slug <> 'new'`); c.ID <= int64(max) {
		t.Fatalf("new card id %d, restored max %d", c.ID, max)
	}
}

func TestRestoreConfig(t *testing.T) {
	_, config, box := source(t)
	tg := newTarget(t)
	tg.pluginRole(t)
	res, err := tg.restore(config, box, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.SecretsKeyMismatch || len(res.SkippedBindings) != 1 || res.Rows["binding"] != 0 || res.Rows["push"] != 0 || len(res.SkippedPluginSchemas) != 0 {
		t.Fatalf("result = %+v", res)
	}
	ctx := context.Background()
	var sealed []byte
	if err := tg.pool.QueryRow(ctx, `SELECT secrets FROM plugin_instance WHERE name = 'fake'`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if s, err := box.Open(sealed); err != nil || s["token"] != "s3cret" {
		t.Fatalf("secrets = %v, %v", s, err)
	}
	var body string
	if err := tg.pool.QueryRow(ctx, `SELECT body FROM plugin_fake.note`).Scan(&body); err != nil || body != "remember the forge" {
		t.Fatalf("plugin row = %q, %v", body, err)
	}
}

func TestRestoreBoth(t *testing.T) {
	assets, config, box := source(t)
	tg := newTarget(t)
	tg.pluginRole(t)
	if _, err := tg.restore(assets, box, false); err != nil {
		t.Fatal(err)
	}
	res, err := tg.restore(config, box, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SkippedBindings) != 0 || res.Rows["binding"] != 1 || res.Rows["push"] != 1 {
		t.Fatalf("result = %+v", res)
	}
	if n := tg.count(t, `SELECT count(*) FROM binding_status WHERE status = 'in_sync'`); n != 1 {
		t.Fatalf("%d in_sync bindings, want 1", n)
	}

	// Replacing config drops the plugin schemas before plugins.sql recreates them.
	if _, err := tg.restore(config, box, false); !errors.Is(err, ErrHasData) {
		t.Fatalf("err = %v, want ErrHasData", err)
	}
	if _, err := tg.restore(config, box, true); err != nil {
		t.Fatal(err)
	}
	if n := tg.count(t, `SELECT count(*) FROM plugin_fake.note`) + tg.count(t, `SELECT count(*) FROM binding_status WHERE status = 'in_sync'`); n != 2 {
		t.Fatalf("after replace: %d plugin rows plus in_sync bindings, want 2", n)
	}
}

func TestRestoreRefusesExistingData(t *testing.T) {
	assets, _, _ := source(t)
	tg := newTarget(t)
	if _, err := tg.pool.Exec(context.Background(), `INSERT INTO collection (name) VALUES ('mine')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tg.restore(assets, nil, false); !errors.Is(err, ErrHasData) {
		t.Fatalf("err = %v, want ErrHasData", err)
	}
	if tg.count(t, `SELECT count(*) FROM card`) != 0 || tg.count(t, `SELECT count(*) FROM collection`) != 1 {
		t.Fatal("restore changed the database")
	}
	if es, _ := os.ReadDir(tg.store.Root); len(es) != 0 {
		t.Fatalf("store has %v", es)
	}
}

func TestRestoreReplaceAssets(t *testing.T) {
	assets, _, _ := source(t)
	tg := newTarget(t)
	if _, _, err := tg.lib.Cards.Create(context.Background(), "other", "upload",
		library.Edit{Data: json.RawMessage(`{"name": "Other"}`), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	// An upload in flight: its row must survive for the card that will use it.
	if _, err := tg.lib.PutBlob(context.Background(), strings.NewReader("pending")); err != nil {
		t.Fatal(err)
	}
	if _, err := tg.restore(assets, nil, true); err != nil {
		t.Fatal(err)
	}
	if s := tg.slugs(t); !slices.Equal(s, []string{"brakka"}) {
		t.Fatalf("cards = %v", s)
	}
	if n := tg.count(t, `SELECT count(*) FROM blob`); n != 2 {
		t.Fatalf("%d blob rows, want the upload's and the avatar's", n)
	}
	checkAssets(t, tg)
}

func TestRestoreReplaceKeepsBindingsWhole(t *testing.T) {
	pool, lib, o := setup(t)
	ctx := context.Background()
	seed(t, lib)
	assets := archive(t, pool, Assets, o)
	// A second card, absent from the archive, with a binding.
	c, _, err := lib.Cards.Create(ctx, "later", "upload", library.Edit{Data: json.RawMessage(`{"name": "Later"}`), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'fake')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data, accepted_hash)
			SELECT id, $1, 'r1', 'unchanged', '{}', 'h' FROM plugin_instance`, c.ID); err != nil {
		t.Fatal(err)
	}
	tg := target{pool, lib, o.Store}
	_, err = tg.restore(assets, nil, true)
	if err == nil || !strings.Contains(err.Error(), "bindings refer to cards or lorebooks") {
		t.Fatalf("err = %v", err)
	}
	if s := tg.slugs(t); !slices.Equal(s, []string{"brakka", "later"}) {
		t.Fatalf("cards = %v", s)
	}
	// The triggers came back with the rollback.
	if _, err := pool.Exec(ctx, `DELETE FROM card_revision`); err == nil {
		t.Fatal("card_revision is no longer immutable")
	}
}

func TestRestoreSecretsKeyMismatch(t *testing.T) {
	_, config, _ := source(t)
	res, err := newTarget(t).restore(config, testBox(t, 2), false)
	if err != nil || !res.SecretsKeyMismatch {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

func TestRestoreRefusesOtherSchema(t *testing.T) {
	assets, _, _ := source(t)
	other := rebuild(t, assets, func(bodies map[string]string) {
		editManifest(t, bodies, func(m *Manifest) { m.Schema = "9999_future.sql" })
	})
	tg := newTarget(t)
	_, err := tg.restore(other, nil, false)
	if err == nil || !strings.Contains(err.Error(), "another Innkeeper version") {
		t.Fatalf("err = %v", err)
	}
	if tg.count(t, `SELECT count(*) FROM card`) != 0 {
		t.Fatal("restore wrote rows")
	}
	if es, _ := os.ReadDir(tg.store.Root); len(es) != 0 {
		t.Fatalf("store has %v", es)
	}
}

func TestRestorePluginSQLRunsAsPluginRole(t *testing.T) {
	_, config, box := source(t)
	for _, hostile := range []string{
		`SELECT * FROM public.card`,
		`CREATE TABLE public.evil ()`,
		`RESET ROLE; CREATE TABLE public.evil ()`,
	} {
		tg := newTarget(t)
		tg.pluginRole(t)
		evil := rebuild(t, config, func(bodies map[string]string) { bodies["plugins/plugin_fake.sql"] = hostile })
		_, err := tg.restore(evil, box, false)
		wantDenied(t, hostile, err)
		if n := tg.count(t, `SELECT count(*) FROM plugin_instance`) + tg.count(t, `SELECT count(*) FROM pg_class WHERE relname = 'evil'`); n != 0 {
			t.Fatalf("%s: restore changed the database", hostile)
		}
	}
}

func TestRestorePluginCantHijackSearchPath(t *testing.T) {
	_, config, box := source(t)
	tg := newTarget(t)
	tg.pluginRole(t)
	// Core runs now() and max() after plugin SQL; these must never be the ones it calls.
	hijack := rebuild(t, config, func(bodies map[string]string) {
		bodies["plugins/plugin_fake.sql"] = `
			CREATE FUNCTION plugin_fake.now() RETURNS timestamptz LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'hijacked'; END$$;
			CREATE FUNCTION plugin_fake.max(bigint) RETURNS bigint LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'hijacked'; END$$;
			SET search_path = plugin_fake, pg_catalog`
	})
	if _, err := tg.restore(hijack, box, false); err != nil {
		t.Fatal(err)
	}
}

func TestRestorePluginCantDeferPastItsRole(t *testing.T) {
	_, config, box := source(t)
	tg := newTarget(t)
	tg.pluginRole(t)
	// It must fire inside the plugin's call. PostgreSQL 18 already runs a
	// deferred trigger as the role that queued it; older versions use the role at commit.
	deferred := rebuild(t, config, func(bodies map[string]string) {
		bodies["plugins/plugin_fake.sql"] = `
			CREATE TABLE plugin_fake.t (x int);
			CREATE FUNCTION plugin_fake.f() RETURNS trigger LANGUAGE plpgsql AS
				$$BEGIN INSERT INTO public.setting VALUES ('evil', 'yes'); RETURN NULL; END$$;
			CREATE CONSTRAINT TRIGGER g AFTER INSERT ON plugin_fake.t DEFERRABLE INITIALLY DEFERRED
				FOR EACH ROW EXECUTE FUNCTION plugin_fake.f();
			INSERT INTO plugin_fake.t VALUES (1) -- trailing comment`
	})
	_, err := tg.restore(deferred, box, false)
	wantDenied(t, "deferred trigger", err)
	if !strings.Contains(err.Error(), "plugins/plugin_fake.sql") {
		t.Fatalf("err = %v, want it from the plugin's SQL, not commit", err)
	}
	if n := tg.count(t, `SELECT count(*) FROM setting`) + tg.count(t, `SELECT count(*) FROM plugin_instance`); n != 0 {
		t.Fatal("restore changed the database")
	}
}

func TestRestoreRejectsOtherTypesEntries(t *testing.T) {
	assets, config, box := source(t)
	tg := newTarget(t)
	tg.pluginRole(t)
	for name, archive := range map[string][]byte{
		"plugin entry in assets": rebuild(t, assets, func(b map[string]string) { b["plugins/plugin_fake.sql"] = "SELECT 1" }),
		"plugin schema in assets manifest": rebuild(t, assets, func(b map[string]string) {
			editManifest(t, b, func(m *Manifest) { m.PluginSchemas = []string{"plugin_fake"} })
			b["plugins/plugin_fake.sql"] = "SELECT 1"
		}),
		"blob in config manifest": rebuild(t, config, func(b map[string]string) {
			editManifest(t, b, func(m *Manifest) { m.Blobs = []string{sha} })
			b["blobs/"+sha] = "x"
		}),
	} {
		if _, err := tg.restore(archive, box, false); err == nil {
			t.Errorf("%s: restore succeeded", name)
		}
	}
	if n := tg.count(t, `SELECT count(*) FROM card`) + tg.count(t, `SELECT count(*) FROM plugin_instance`); n != 0 {
		t.Fatal("restore changed the database")
	}
}

func TestRestoreKeepsPluginData(t *testing.T) {
	_, config, box := source(t)
	tg := newTarget(t)
	tg.pluginRole(t)
	if _, err := tg.pool.Exec(context.Background(), `CREATE SCHEMA plugin_fake; CREATE TABLE plugin_fake.mine (x int)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tg.restore(config, box, false); !errors.Is(err, ErrHasData) {
		t.Fatalf("err = %v, want ErrHasData", err)
	}
	if n := tg.count(t, `SELECT count(*) FROM pg_class WHERE relname = 'mine'`); n != 1 {
		t.Fatal("restore dropped plugin data")
	}
}

func TestRestoreSkipsPluginWithoutRole(t *testing.T) {
	_, config, box := source(t)
	tg := newTarget(t)
	res, err := tg.restore(config, box, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.SkippedPluginSchemas, []string{"plugin_fake"}) || res.Rows["plugin_instance"] != 1 {
		t.Fatalf("result = %+v", res)
	}
	if n := tg.count(t, `SELECT count(*) FROM pg_namespace WHERE nspname = 'plugin_fake'`); n != 0 {
		t.Fatal("restored a schema no role can own")
	}
}

func TestRestoreKeepsLeftoverBlobRows(t *testing.T) {
	assets, _, _ := source(t)
	tg := newTarget(t)
	// The avatar's row, left by an upload no card uses yet.
	if _, err := tg.lib.PutBlob(context.Background(), strings.NewReader("avatar")); err != nil {
		t.Fatal(err)
	}
	res, err := tg.restore(assets, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows["blob"] != 0 || tg.count(t, `SELECT count(*) FROM blob`) != 1 {
		t.Fatalf("result = %+v", res)
	}
	checkAssets(t, tg)
}

func TestFailedRestoreRemovesItsBlobFiles(t *testing.T) {
	assets, _, _ := source(t)
	tg := newTarget(t)
	// Written after the blobs and tables, so the restore fails having put both.
	bad := rebuild(t, assets, func(b map[string]string) { b["zzz"] = "x" })
	kept, _, err := tg.store.Put(strings.NewReader("here before"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tg.restore(bad, nil, false); err == nil || !strings.Contains(err.Error(), "zzz") {
		t.Fatalf("err = %v, want the bad entry", err)
	}
	var shas []string
	filepath.WalkDir(tg.store.Root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && validSHA(d.Name()) {
			shas = append(shas, d.Name())
		}
		return nil
	})
	if !slices.Equal(shas, []string{kept}) {
		t.Fatalf("store has %v, want only %s", shas, kept)
	}
}

func TestRestoreReportsMissingRows(t *testing.T) {
	assets, _, _ := source(t)
	// collection_card still points at the collection.
	noCollections := rebuild(t, assets, func(b map[string]string) { b["tables/collection.copy"] = "" })
	_, err := newTarget(t).restore(noCollections, nil, false)
	if err == nil || !strings.Contains(err.Error(), "backup refers to rows it doesn't contain") {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreRefusesOversizeBlob(t *testing.T) {
	tg := newTarget(t)
	var schema string
	if err := tg.pool.QueryRow(context.Background(), `SELECT max(name) FROM schema_migration`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	m, _ := json.Marshal(Manifest{Format: 1, Type: Assets, Schema: schema, Tables: map[string]int64{}, Blobs: []string{sha}})
	// Only the blob's header: restore must refuse before reading its body.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "manifest.json", Size: int64(len(m)), Mode: 0o600, Typeflag: tar.TypeReg})
	tw.Write(m)
	tw.WriteHeader(&tar.Header{Name: "blobs/" + sha, Size: maxBlob + 1, Mode: 0o600, Typeflag: tar.TypeReg})
	tw.Flush()
	gz.Close()
	if _, err := tg.restore(buf.Bytes(), nil, false); err == nil || !strings.Contains(err.Error(), "over 256 MiB") {
		t.Fatalf("err = %v", err)
	}
	if es, _ := os.ReadDir(tg.store.Root); len(es) != 0 {
		t.Fatalf("store has %v", es)
	}
}

func TestRemoveUnclaimedLogsFailure(t *testing.T) {
	conn, err := pgx.ParseConfig("postgres://nobody@127.0.0.1:1/none?connect_timeout=5")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	removeUnclaimed(RestoreOptions{Conn: conn, Log: slog.New(slog.NewTextHandler(&out, nil))}, []string{sha})
	if !strings.Contains(out.String(), "can't remove the blob files") {
		t.Fatalf("log = %q", out.String())
	}
}

func TestStripUnrunnable(t *testing.T) {
	long := strings.Repeat("x", 100<<10) + "\n"
	in := "\\restrict abc\nCREATE SCHEMA plugin_fake;\nSELECT 1;\n" + long + "CREATE SCHEMA plugin_fake;x\n\\unrestrict abc"
	got, err := stripUnrunnable(strings.NewReader(in), int64(len(in)))
	if want := "SELECT 1;\n" + long + "CREATE SCHEMA plugin_fake;x\n"; err != nil || got != want {
		t.Fatalf("got %d bytes starting %q, %v", len(got), got[:min(len(got), 40)], err)
	}
}

func TestValidSHA(t *testing.T) {
	if !validSHA(sha) {
		t.Error("rejected a valid hash")
	}
	for _, s := range []string{"", "abcd", strings.ToUpper(sha), sha + "0", filepath.Join("ab", sha[3:])} {
		if validSHA(s) {
			t.Errorf("accepted %q", s)
		}
	}
}
