package plugins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDirSkipsBadPlugins(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, filepath.Join(dir, "good"), `name = "good"
version = "1.0.0"
protocol = 1
command = ["./good"]
capabilities = ["import"]
`)
	writeManifest(t, filepath.Join(dir, "bad"), `name = "bad"
protocol = 99
`)
	if err := os.WriteFile(filepath.Join(dir, "index.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDir(dir, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["good"] == nil {
		t.Fatalf("loaded %v, want only good", got)
	}
}

func newRegistry(t *testing.T) (*Registry, *blobstore.Store) {
	t.Helper()
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	m := &protocol.Manifest{Name: "fake", Version: "0.0.1", Secrets: []string{"api_key", "region"}, MultipleInstances: true}
	dusk := &protocol.Manifest{Name: "dusk", Version: "1.0.0", Capabilities: []string{"theme"}, Stylesheet: "theme.css"}
	return New(testdb.New(t), map[string]*protocol.Manifest{"fake": m, "dusk": dusk}, store, box), store
}

func TestInstance(t *testing.T) {
	ctx := context.Background()
	reg, store := newRegistry(t)

	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "missing", Name: new("x")}); !errors.Is(err, ErrUnknownPlugin) {
		t.Fatalf("create for missing plugin: %v", err)
	}
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake"),
		Config: map[string]any{"path": "/cards"}, Secrets: map[string]string{"api_key": "hunter2"}})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Manifest != reg.Manifests()["fake"] || inst.Params.Config["path"] != "/cards" ||
		inst.Params.BlobTmp != store.TmpDir() || inst.Params.Secrets["api_key"] != "hunter2" || inst.Version == "" {
		t.Fatalf("instance = %+v", inst)
	}
	if _, err := os.Stat(store.TmpDir()); err != nil {
		t.Fatalf("blob tmp folder: %v", err)
	}

	if err := reg.MarkBroken(ctx, id, inst.Version, errors.New("wrong protocol")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Instance(ctx, id); !errors.Is(err, ErrBroken) {
		t.Fatalf("broken instance: %v", err)
	}
	if _, err := reg.Instance(ctx, id+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing instance: %v", err)
	}
}

func TestInstanceInfoHidesSecretValues(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	owner := "owner"
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake"),
		Secrets: map[string]string{"region": "eu", "api_key": "hunter2"}, OwnerSubject: &owner})
	if err != nil {
		t.Fatal(err)
	}
	info, err := reg.GetInstance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(info.Secrets, []string{"api_key", "region"}) || *info.OwnerSubject != "owner" || info.Config == nil {
		t.Fatalf("info = %+v", info)
	}
	if strings.Contains(fmt.Sprintf("%+v", info), "hunter2") {
		t.Fatal("instance info contains a secret value")
	}
	list, err := reg.ListInstances(ctx)
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestCreateInstanceRejectsBadSettings(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake"),
		Secrets: map[string]string{"password": "x"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("undeclared secret: %v", err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake")}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake")}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate name: %v", err)
	}
}

func TestUpdateInstance(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake"),
		Config: map[string]any{"path": "/a"}, Secrets: map[string]string{"api_key": "old", "region": "eu"}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.MarkBroken(ctx, id, before.Version, errors.New("bad key")); err != nil {
		t.Fatal(err)
	}
	newKey, name, owner := "new", "Renamed", "owner"
	err = reg.UpdateInstance(ctx, id, InstanceEdit{Name: &name, OwnerSubject: &owner,
		Secrets: map[string]*string{"api_key": &newKey, "region": nil}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatalf("instance still broken after an edit: %v", err)
	}
	if after.Version == before.Version {
		t.Fatal("version didn't change")
	}
	if err := reg.MarkBroken(ctx, id, before.Version, errors.New("stale start failure")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Instance(ctx, id); err != nil {
		t.Fatalf("stale MarkBroken broke the edited instance: %v", err)
	}
	if after.Params.Secrets["api_key"] != "new" || len(after.Params.Secrets) != 1 || after.Params.Config["path"] != "/a" {
		t.Fatalf("after edit: %+v", after.Params)
	}
	info, err := reg.GetInstance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if info.Label != "Renamed" || *info.OwnerSubject != "owner" || info.BrokenError != nil {
		t.Fatalf("info = %+v", info)
	}

	other := "Other"
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Name: &other}); err != nil {
		t.Fatal(err)
	}
	if info, _ := reg.GetInstance(ctx, id); info.OwnerSubject == nil || *info.OwnerSubject != "owner" {
		t.Fatalf("name-only edit changed the owner: %+v", info.OwnerSubject)
	}
	if kept, err := reg.Instance(ctx, id); err != nil || kept.Params.Secrets["api_key"] != "new" {
		t.Fatalf("name-only edit changed secrets: %+v, %v", kept.Params.Secrets, err)
	}
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Config: map[string]any{"path": "/b"}}); err != nil {
		t.Fatal(err)
	}
	if replaced, err := reg.Instance(ctx, id); err != nil || replaced.Params.Config["path"] != "/b" {
		t.Fatalf("config edit: %+v, %v", replaced.Params.Config, err)
	}

	empty := ""
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{OwnerSubject: &empty}); err != nil {
		t.Fatal(err)
	}
	if info, _ := reg.GetInstance(ctx, id); info.OwnerSubject != nil {
		t.Fatalf("owner not cleared: %q", *info.OwnerSubject)
	}
	bad := "x"
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Secrets: map[string]*string{"password": &bad}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("undeclared secret: %v", err)
	}
	if err := reg.UpdateInstance(ctx, id+1, InstanceEdit{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing instance: %v", err)
	}
}

func TestDeleteInstance(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake")})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.DeleteInstance(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := reg.DeleteInstance(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestDeleteInstanceWithBindings(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.pool.Exec(ctx, `WITH c AS (INSERT INTO card (slug, provenance) VALUES ('a', 'upload') RETURNING id)
		INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data, accepted_hash)
		SELECT $1, id, 'r1', 'unchanged', '{}', '' FROM c`, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.DeleteInstance(ctx, id); !errors.Is(err, ErrInUse) {
		t.Fatalf("delete: %v, want ErrInUse", err)
	}
}

func TestDeleteImporterDropsItsImportBindings(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.pool.Exec(ctx, `WITH c AS (INSERT INTO card (slug, provenance) VALUES ('a', 'fake:a') RETURNING id)
		INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data, accepted_hash, push, imported)
		SELECT $1, id, 'fake:a', 'unchanged', '{}', '', false, true FROM c`, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.DeleteInstance(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestUpdateInstanceEndsSessions(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake")})
	if err != nil {
		t.Fatal(err)
	}
	sessions := func() int {
		var n int
		if err := reg.pool.QueryRow(ctx, `SELECT count(*) FROM session WHERE instance_id = $1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	addSession := func() {
		if _, err := reg.pool.Exec(ctx, `INSERT INTO session (token_hash, instance_id, subject, expires_at)
			VALUES ('h', $1, 'owner', now() + interval '1 day') ON CONFLICT DO NOTHING`, id); err != nil {
			t.Fatal(err)
		}
	}
	name, owner, key, off := "Other", "owner", "k", true
	edits := map[string]InstanceEdit{
		"disabled": {Disabled: &off},
		"owner":    {OwnerSubject: &owner},
		"config":   {Config: map[string]any{"a": 1}},
		"secrets":  {Secrets: map[string]*string{"api_key": &key}},
	}
	addSession()
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Name: &name}); err != nil || sessions() != 1 {
		t.Fatalf("name-only edit: sessions = %d, err = %v", sessions(), err)
	}
	for what, e := range edits {
		addSession()
		if err := reg.UpdateInstance(ctx, id, e); err != nil {
			t.Fatal(err)
		}
		if n := sessions(); n != 0 {
			t.Fatalf("%s edit left %d sessions", what, n)
		}
	}
}

func TestTheme(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	if name, err := reg.Theme(ctx); err != nil || name != "" {
		t.Fatalf("default theme = %q, %v", name, err)
	}
	if err := reg.SetTheme(ctx, "dusk"); err != nil {
		t.Fatal(err)
	}
	if name, _ := reg.Theme(ctx); name != "dusk" {
		t.Fatalf("theme = %q, want dusk", name)
	}
	for _, bad := range []string{"fake", "missing"} {
		if err := reg.SetTheme(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("SetTheme(%q) = %v, want ErrInvalid", bad, err)
		}
	}
	if err := reg.SetTheme(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if name, _ := reg.Theme(ctx); name != "" {
		t.Fatalf("theme after reset = %q", name)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "dusk", Name: new("Dusk")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("theme instance: %v", err)
	}
}

func manifestFor(name, version string) string {
	return fmt.Sprintf("name = %q\nversion = %q\nprotocol = 1\ncommand = [\"./%s\"]\ncapabilities = [\"import\"]\n",
		name, version, name)
}

func TestLoadInstalled(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, filepath.Join(dir, "good", "local@1.0.0"), manifestFor("good", "1.0.0"))
	writeManifest(t, filepath.Join(dir, "good", "local@2.0.0"), manifestFor("good", "2.0.0"))
	if err := os.Symlink("local@2.0.0", filepath.Join(dir, "good", "current")); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, filepath.Join(dir, "nocurrent", "local@1.0.0"), manifestFor("nocurrent", "1.0.0"))
	writeManifest(t, filepath.Join(dir, "misnamed", "local@1.0.0"), manifestFor("other", "1.0.0"))
	if err := os.Symlink("local@1.0.0", filepath.Join(dir, "misnamed", "current")); err != nil {
		t.Fatal(err)
	}

	got, err := LoadInstalled(dir, quiet)
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["good"] == nil || got["good"].Version != "2.0.0" {
		t.Fatalf("loaded %v, want only good 2.0.0", got)
	}
	if want := filepath.Join(real, "good", "local@2.0.0"); got["good"].Dir != want {
		t.Fatalf("Dir = %s, want %s", got["good"].Dir, want)
	}

	none, err := LoadInstalled(filepath.Join(dir, "missing"), quiet)
	if err != nil || len(none) != 0 {
		t.Fatalf("missing dir: %v, %v", none, err)
	}
}

// openRegistry opens a registry over pool with a built-in "login" plugin and
// an empty installed folder, and returns it with its dirs.
func openRegistry(t *testing.T, pool *pgxpool.Pool) (*Registry, Dirs) {
	t.Helper()
	dirs := Dirs{Builtin: t.TempDir(), Installed: t.TempDir()}
	writeManifest(t, filepath.Join(dirs.Builtin, "login"), manifestFor("login", "1.0.0"))
	return openWith(t, pool, dirs), dirs
}

func openWith(t *testing.T, pool *pgxpool.Pool, dirs Dirs) *Registry {
	t.Helper()
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := Open(context.Background(), pool, dirs, &blobstore.Store{Name: "library", Root: t.TempDir()}, box, "", quiet)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestSyncReloadsAfterGeneration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	reg, dirs := openRegistry(t, pool)
	if m := reg.Manifests()["login"]; m == nil || m.Version != "1.0.0" || !reg.IsBuiltin("login") {
		t.Fatalf("login = %+v, want built-in 1.0.0", m)
	}

	base := filepath.Join(dirs.Installed, "login")
	writeManifest(t, filepath.Join(base, "local@2.0.0"), manifestFor("login", "2.0.0"))
	if err := os.Symlink("local@2.0.0", filepath.Join(base, "current")); err != nil {
		t.Fatal(err)
	}
	if err := reg.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if v := reg.Manifests()["login"].Version; v != "1.0.0" {
		t.Fatalf("reloaded without a new generation: %s", v)
	}
	if _, err := pool.Exec(ctx, `SELECT nextval('plugin_generation')`); err != nil {
		t.Fatal(err)
	}
	if err := reg.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if v := reg.Manifests()["login"].Version; v != "2.0.0" {
		t.Fatalf("after a new generation, login = %s, want the installed 2.0.0", v)
	}
}

func TestInstanceVersionFollowsPluginChange(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake")})
	if err != nil {
		t.Fatal(err)
	}
	before, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.pool.Exec(ctx, `INSERT INTO plugin_install (name, changed_at) VALUES ('fake', now() + interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	after, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := strconv.ParseInt(before.Version, 10, 64)
	a, _ := strconv.ParseInt(after.Version, 10, 64)
	if a <= b {
		t.Fatalf("version %d after a plugin change, want more than %d", a, b)
	}
	// A failure at the old version must not mark the instance broken.
	if err := reg.MarkBroken(ctx, id, before.Version, errors.New("old")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Instance(ctx, id); err != nil {
		t.Fatalf("stale MarkBroken broke the instance: %v", err)
	}
	if err := reg.MarkBroken(ctx, id, after.Version, errors.New("new")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Instance(ctx, id); !errors.Is(err, ErrBroken) {
		t.Fatalf("got %v, want ErrBroken", err)
	}
}

func TestInstancesWithEveryCapability(t *testing.T) {
	ctx := context.Background()
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{
		"app":    {Name: "app", Capabilities: []string{"push", "pull"}},
		"pusher": {Name: "pusher", Capabilities: []string{"push"}},
	}, &blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	app, err := reg.CreateInstance(ctx, NewInstance{Plugin: "app", Name: new("App")})
	if err != nil {
		t.Fatal(err)
	}
	pusher, err := reg.CreateInstance(ctx, NewInstance{Plugin: "pusher", Name: new("Pusher")})
	if err != nil {
		t.Fatal(err)
	}

	list, err := reg.InstancesWith(ctx, "push", "pull")
	if err != nil || len(list) != 1 || list[0].ID != app {
		t.Fatalf("InstancesWith(push, pull) = %+v, %v; want only %d", list, err, app)
	}
	if list, err := reg.InstancesWith(ctx, "push"); err != nil || len(list) != 2 {
		t.Fatalf("InstancesWith(push) = %+v, %v; want both", list, err)
	}
	if _, err := reg.InstanceWith(ctx, app, "pull"); err != nil {
		t.Fatalf("InstanceWith(app, pull): %v", err)
	}
	if _, err := reg.InstanceWith(ctx, pusher, "pull"); !errors.Is(err, ErrIncapable) {
		t.Fatalf("InstanceWith(pusher, pull): %v, want ErrIncapable", err)
	}
}

func TestInstanceKeepsHashOfReplacedDefault(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	reg.manifests["fake"].Config = map[string]any{"type": "object", "properties": map[string]any{
		"p": map[string]any{"type": "object", "properties": map[string]any{
			"a": map[string]any{"type": "string", "default": "new A"},
			"b": map[string]any{"type": "string", "default": "new B"},
		}},
	}}
	updated := func(id int64) []string {
		t.Helper()
		got, err := reg.Defaults(ctx, nil, "fake", &id)
		if err != nil {
			t.Fatal(err)
		}
		return got.Updated
	}
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake"),
		Config: map[string]any{"p": map[string]any{"a": "mine"}}, DefaultBases: map[string]any{"/p/a": "old A"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := updated(id); !slices.Equal(got, []string{"/p/a"}) {
		t.Fatalf("after create: updated = %v", got)
	}

	// A save that leaves /p/a alone keeps its base; a new override gets its own.
	cfg := map[string]any{"p": map[string]any{"a": "mine", "b": "also mine"}}
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Config: cfg, DefaultBases: map[string]any{"/p/b": "new B"}}); err != nil {
		t.Fatal(err)
	}
	if got := updated(id); !slices.Equal(got, []string{"/p/a"}) {
		t.Fatalf("after adding b: updated = %v", got)
	}

	// Resetting /p/a drops its hash.
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Config: map[string]any{"p": map[string]any{"b": "also mine"}}}); err != nil {
		t.Fatal(err)
	}
	if got := updated(id); len(got) != 0 {
		t.Fatalf("after reset: updated = %v", got)
	}
}

func TestCreateInstanceRejectsSecondSingleton(t *testing.T) {
	ctx := context.Background()
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{
		"solo": {Name: "solo", Version: "1", Capabilities: []string{"import"}},
		"many": {Name: "many", Version: "1", Capabilities: []string{"push"}, MultipleInstances: true},
	}, &blobstore.Store{Name: "library", Root: t.TempDir()}, box)

	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "solo", Name: new("solo")}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "solo", Name: new("solo 2")}); !errors.Is(err, ErrSingleton) {
		t.Fatalf("second solo instance: err = %v, want ErrSingleton", err)
	}
	for _, name := range []string{"a", "b"} {
		if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "many", Name: new(name)}); err != nil {
			t.Fatalf("many instance %s: %v", name, err)
		}
	}
}

func TestInstanceNamesAreUniqueWithinAPlugin(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	create := func(plugin string, name *string) error {
		_, err := reg.CreateInstance(ctx, NewInstance{Plugin: plugin, Name: name})
		return err
	}

	if err := create("fake", nil); err != nil {
		t.Fatal(err)
	}
	if err := create("fake", nil); !errors.Is(err, ErrNeedsName) {
		t.Fatalf("second unnamed instance: err = %v, want ErrNeedsName", err)
	}
	if err := create("fake", new("Home")); err != nil {
		t.Fatal(err)
	}
	if err := create("fake", new("Home")); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("second Home: err = %v, want ErrNameTaken", err)
	}
	list, err := reg.ListInstances(ctx)
	if err != nil || len(list) != 2 || list[0].Label != "fake" || list[1].Label != "Home" {
		t.Fatalf("instances = %+v, %v; want labels fake and Home", list, err)
	}
	if err := reg.UpdateInstance(ctx, list[1].ID, InstanceEdit{Name: new("")}); !errors.Is(err, ErrNeedsName) {
		t.Fatalf("clearing Home's name beside an unnamed instance: err = %v, want ErrNeedsName", err)
	}
	if err := reg.DeleteInstance(ctx, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := reg.UpdateInstance(ctx, list[1].ID, InstanceEdit{Name: new("")}); err != nil {
		t.Fatal(err)
	}
	if got, err := reg.GetInstance(ctx, list[1].ID); err != nil || got.Name != nil || got.Label != "fake" {
		t.Fatalf("after clearing its name: %+v, %v", got, err)
	}
}

func TestSingletonInstanceHasNoName(t *testing.T) {
	ctx := context.Background()
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{
		"solo": {Name: "solo", Version: "1", Capabilities: []string{"import"}},
	}, &blobstore.Store{Name: "library", Root: t.TempDir()}, box)

	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "solo", Name: new("Mine")})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Name: new("Renamed")}); err != nil {
		t.Fatal(err)
	}
	if got, err := reg.GetInstance(ctx, id); err != nil || got.Name != nil || got.Label != "solo" {
		t.Fatalf("singleton instance = %+v, %v; want no name, label solo", got, err)
	}
}

func TestPluginStatus(t *testing.T) {
	ok := InstanceInfo{ID: 1, Status: "ready"}
	bad := InstanceInfo{ID: 2, Status: "broken"}
	unset := InstanceInfo{ID: 3, Status: "needs_setup"}
	off := InstanceInfo{ID: 4, Status: "disabled"}
	solo := &protocol.Manifest{Name: "solo", Capabilities: []string{"import"}}
	many := &protocol.Manifest{Name: "many", Capabilities: []string{"push"}, MultipleInstances: true}
	theme := &protocol.Manifest{Name: "dusk", Capabilities: []string{"theme"}}
	tests := []struct {
		name      string
		m         *protocol.Manifest
		instances []InstanceInfo
		want      string
	}{
		{"theme", theme, nil, "ready"},
		{"singleton with no instance", solo, nil, "needs_setup"},
		{"multi with no instance", many, nil, "needs_setup"},
		{"one ready", solo, []InstanceInfo{ok}, "ready"},
		{"broken wins", many, []InstanceInfo{ok, unset, bad}, "broken"},
		{"needs setup beats ready", many, []InstanceInfo{ok, unset}, "needs_setup"},
		{"all disabled", many, []InstanceInfo{off, off}, "disabled"},
		{"one disabled, one ready", many, []InstanceInfo{off, ok}, "ready"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Status(tt.m, tt.instances); got != tt.want {
				t.Fatalf("Status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInstanceStatus(t *testing.T) {
	ctx := context.Background()
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	pool := testdb.New(t)
	reg := New(pool, map[string]*protocol.Manifest{
		"conf": {Name: "conf", Version: "1", Capabilities: []string{"import"}, MultipleInstances: true,
			Config: map[string]any{"type": "object", "required": []any{"url"}}},
	}, &blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	create := func(name string, config map[string]any) int64 {
		id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "conf", Name: new(name), Config: config})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ready := create("ready", map[string]any{"url": "https://x"})
	missing := create("missing", nil)
	broken := create("broken", map[string]any{"url": "https://x"})
	if _, err := pool.Exec(ctx, `UPDATE plugin_instance SET broken_error = 'boom', broken_at = now() WHERE id = $1`, broken); err != nil {
		t.Fatal(err)
	}
	off := create("off", map[string]any{"url": "https://x"})
	if _, err := pool.Exec(ctx, `UPDATE plugin_instance SET disabled = true, broken_error = 'boom' WHERE id = $1`, off); err != nil {
		t.Fatal(err)
	}
	var ghost int64
	if err := pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name) VALUES ('ghost', 'ghost') RETURNING id`).Scan(&ghost); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{ready: "ready", missing: "needs_setup", broken: "broken", off: "disabled", ghost: "broken"} {
		got, err := reg.GetInstance(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want {
			t.Errorf("instance %s: status %q, want %q", got.Label, got.Status, want)
		}
	}
}

func TestDisabledInstanceIsSkipped(t *testing.T) {
	ctx := context.Background()
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	pool := testdb.New(t)
	reg := New(pool, map[string]*protocol.Manifest{
		"codec": {Name: "codec", Version: "1", Capabilities: []string{"codec"}, Formats: []string{"png"}},
	}, &blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "codec", Name: new("codec")})
	if err != nil {
		t.Fatal(err)
	}
	disabled := true
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Instance(ctx, id); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Instance: err = %v, want ErrDisabled", err)
	}
	if _, err := reg.InstanceWith(ctx, id, "codec"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("InstanceWith: err = %v, want ErrDisabled", err)
	}
	if _, err := reg.CodecInstance(ctx, "png"); !errors.Is(err, ErrNoCodec) {
		t.Fatalf("CodecInstance: err = %v, want ErrNoCodec", err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "codec", Name: new("codec 2")}); !errors.Is(err, ErrSingleton) {
		t.Fatalf("second instance while disabled: err = %v, want ErrSingleton", err)
	}

	enabled := false
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Disabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CodecInstance(ctx, "png"); err != nil {
		t.Fatalf("after Enable: CodecInstance err = %v", err)
	}
}

func TestConcurrentDisablesKeepOneLogin(t *testing.T) {
	ctx := context.Background()
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{
		"login": {Name: "login", Version: "1", Capabilities: []string{"auth"}},
	}, &blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	owner, disabled := "owner", true
	// The race window is narrow, so repeat with fresh pairs of logins.
	for round := range 30 {
		if _, err := reg.pool.Exec(ctx, `DELETE FROM plugin_instance`); err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, name := range []string{"a", "b"} {
			id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "login", Name: new(name), OwnerSubject: &owner})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		start := make(chan struct{})
		errs := make(chan error, len(ids))
		for _, id := range ids {
			go func() {
				<-start
				errs <- reg.UpdateInstance(ctx, id, InstanceEdit{Disabled: &disabled})
			}()
		}
		close(start)
		var refused int
		for range ids {
			if err := <-errs; errors.Is(err, ErrLastLogin) {
				refused++
			} else if err != nil {
				t.Fatal(err)
			}
		}
		var usable int
		if err := reg.pool.QueryRow(ctx, `SELECT count(*) FROM plugin_instance WHERE NOT disabled`).Scan(&usable); err != nil {
			t.Fatal(err)
		}
		if refused != 1 || usable != 1 {
			t.Fatalf("round %d: %d refused, %d usable logins, want 1 and 1", round, refused, usable)
		}
	}
}
