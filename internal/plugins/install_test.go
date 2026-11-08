package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

func folders(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestSources(t *testing.T) {
	ctx := context.Background()
	reg, _ := openRegistry(t, testdb.New(t))
	if err := reg.AddSource(ctx, PluginSource{ID: "builtin", URL: "file:///x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("builtin id: %v", err)
	}
	if err := reg.AddSource(ctx, PluginSource{ID: "web", URL: "http://example.com/index.toml"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("http URL: %v", err)
	}
	if err := reg.AddSource(ctx, PluginSource{ID: "local", URL: "file:///srv/index.toml"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.AddSource(ctx, PluginSource{ID: "local", URL: "file:///other.toml"}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate id: %v", err)
	}
	got, err := reg.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "official" || got[1].ID != "local" {
		t.Fatalf("sources = %+v", got)
	}
	if err := reg.DeleteSource(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	if err := reg.DeleteSource(ctx, "local"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestInstallUpdateRollback(t *testing.T) {
	ctx := context.Background()
	reg, dirs := openRegistry(t, testdb.New(t))
	src := testutil.PluginSource(t,
		testutil.PluginRelease{Name: "good", Version: "1.0.0"},
		testutil.PluginRelease{Name: "good", Version: "2.0.0"})
	if err := reg.AddSource(ctx, PluginSource{ID: "local", URL: src}); err != nil {
		t.Fatal(err)
	}

	if err := reg.Install(ctx, "local", "good", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if m := reg.Manifests()["good"]; m == nil || m.Version != "1.0.0" {
		t.Fatalf("after install: %+v", m)
	}
	if err := reg.Install(ctx, "local", "good", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	ins, err := reg.Installs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if in := ins["good"]; in.Version != "2.0.0" || in.PreviousVersion == nil || *in.PreviousVersion != "1.0.0" {
		t.Fatalf("after update: %+v", in)
	}

	// Rollback reuses the folder on disk, so it works with the source gone.
	if err := os.RemoveAll(filepath.Dir(src[len("file://"):])); err != nil {
		t.Fatal(err)
	}
	if err := reg.Install(ctx, "local", "good", "1.0.0"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if v := reg.Manifests()["good"].Version; v != "1.0.0" {
		t.Fatalf("after rollback: %s", v)
	}
	got := folders(t, filepath.Join(dirs.Installed, "good"))
	if !slices.Equal(got, []string{"current", "local@1.0.0", "local@2.0.0"}) {
		t.Fatalf("folders = %v", got)
	}
}

func TestInstallRefusesBadArtifacts(t *testing.T) {
	ctx := context.Background()
	reg, dirs := openRegistry(t, testdb.New(t))
	src := testutil.PluginSource(t,
		testutil.PluginRelease{Name: "tampered", Version: "1.0.0", SHA256: "00"},
		testutil.PluginRelease{Name: "future", Version: "1.0.0", Protocol: 99})
	if err := reg.AddSource(ctx, PluginSource{ID: "local", URL: src}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Install(ctx, "local", "tampered", "1.0.0"); !errors.Is(err, ErrSource) {
		t.Fatalf("bad hash: %v", err)
	}
	if err := reg.Install(ctx, "local", "future", "1.0.0"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("other protocol: %v", err)
	}
	if err := reg.Install(ctx, "local", "missing", "1.0.0"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("not in index: %v", err)
	}
	if err := reg.Install(ctx, "local", "../escape", "1.0.0"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad name: %v", err)
	}
	for _, name := range []string{"tampered", "future"} {
		if reg.Manifests()[name] != nil {
			t.Fatalf("%s installed", name)
		}
		if leftovers, _ := os.ReadDir(filepath.Join(dirs.Installed, name)); len(leftovers) != 0 {
			t.Fatalf("%s left %v", name, leftovers)
		}
	}
}

func TestSyncSeesOtherProcessInstall(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	api, dirs := openRegistry(t, pool)
	worker := openWith(t, pool, dirs)
	src := testutil.PluginSource(t, testutil.PluginRelease{Name: "good", Version: "1.0.0"})
	if err := api.AddSource(ctx, PluginSource{ID: "local", URL: src}); err != nil {
		t.Fatal(err)
	}
	if err := api.Install(ctx, "local", "good", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := worker.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if worker.Manifests()["good"] == nil {
		t.Fatal("worker didn't load the plugin the API installed")
	}
}

func TestUntarRefusesEscape(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "../evil", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	dir := t.TempDir()
	if err := untar(&buf, filepath.Join(dir, "in")); err == nil {
		t.Fatal("untar wrote outside its folder")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
		t.Fatal("evil exists")
	}
}

func TestUninstallKeepsInstances(t *testing.T) {
	ctx := context.Background()
	reg, dirs := openRegistry(t, testdb.New(t))
	src := testutil.PluginSource(t,
		testutil.PluginRelease{Name: "good", Version: "1.0.0"},
		testutil.PluginRelease{Name: "login", Version: "2.0.0"})
	if err := reg.AddSource(ctx, PluginSource{ID: "local", URL: src}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Install(ctx, "local", "good", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "good", Name: new("Good")})
	if err != nil {
		t.Fatal(err)
	}

	if err := reg.Uninstall(ctx, "good"); err != nil {
		t.Fatal(err)
	}
	if reg.Manifests()["good"] != nil {
		t.Fatal("still loaded")
	}
	if _, err := os.Stat(filepath.Join(dirs.Installed, "good")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("folder still there: %v", err)
	}
	if _, err := reg.Instance(ctx, id); !errors.Is(err, ErrUnknownPlugin) {
		t.Fatalf("instance of uninstalled plugin: %v", err)
	}
	if err := reg.Install(ctx, "local", "good", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Instance(ctx, id); err != nil {
		t.Fatalf("reinstall didn't bring the instance back: %v", err)
	}

	// A built-in plugin can't be uninstalled, but an install over it can.
	if err := reg.Uninstall(ctx, "login"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("uninstall built-in: %v", err)
	}
	if err := reg.Install(ctx, "local", "login", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := reg.Uninstall(ctx, "login"); err != nil {
		t.Fatal(err)
	}
	if v := reg.Manifests()["login"].Version; v != "1.0.0" {
		t.Fatalf("after uninstalling the override, login = %s, want the built-in 1.0.0", v)
	}
	if err := reg.Uninstall(ctx, "never"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uninstall unknown: %v", err)
	}
}

func TestUninstallRemovesAPluginThatDidntLoad(t *testing.T) {
	ctx := context.Background()
	dirs := Dirs{Installed: t.TempDir()}
	// No install row and a manifest that doesn't load, as when a plugin's code outlived its row.
	writeManifest(t, filepath.Join(dirs.Installed, "stale", "official@0.1.0"), "name = \"stale\"\n")
	if err := os.Symlink("official@0.1.0", filepath.Join(dirs.Installed, "stale", "current")); err != nil {
		t.Fatal(err)
	}
	reg := openWith(t, testdb.New(t), dirs)
	if reg.Unloaded()["stale"].Error == "" {
		t.Fatalf("unloaded = %v, want stale's error", reg.Unloaded())
	}

	if err := reg.Uninstall(ctx, "stale"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dirs.Installed, "stale")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("folder still there: %v", err)
	}
	if _, ok := reg.Unloaded()["stale"]; ok {
		t.Fatal("still listed as unloaded")
	}
}

func TestInstallRefusesANewerProtocolBeforeDownloading(t *testing.T) {
	ctx := context.Background()
	reg, _ := openRegistry(t, testdb.New(t))
	// The wrong hash fails any download as ErrSource, so ErrInvalid shows the check came first.
	src := testutil.PluginSource(t, testutil.PluginRelease{Name: "next", Version: "1.0.0", ProtocolVersion: "1.99", SHA256: "00"})
	if err := reg.AddSource(ctx, PluginSource{ID: "local", URL: src}); err != nil {
		t.Fatal(err)
	}
	err := reg.Install(ctx, "local", "next", "1.0.0")
	var newer *protocol.NewerProtocolError
	if !errors.Is(err, ErrInvalid) || !errors.As(err, &newer) || newer.Needs != "1.99" {
		t.Fatalf("install = %v; want ErrInvalid needing protocol 1.99", err)
	}
}

func TestInstallRefusesATarballWhoseProtocolVersionDiffers(t *testing.T) {
	ctx := context.Background()
	reg, _ := openRegistry(t, testdb.New(t))
	current := fmt.Sprintf("%d.%d", protocol.Version, protocol.Minor)
	src := testutil.PluginSource(t,
		testutil.PluginRelease{Name: "same", Version: "1.0.0", ProtocolVersion: current},
		testutil.PluginRelease{Name: "differs", Version: "1.0.0", ManifestProtocolVersion: current})
	if err := reg.AddSource(ctx, PluginSource{ID: "local", URL: src}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Install(ctx, "local", "same", "1.0.0"); err != nil {
		t.Fatalf("matching protocol_version: %v", err)
	}
	if err := reg.Install(ctx, "local", "differs", "1.0.0"); !errors.Is(err, ErrSource) || !strings.Contains(err.Error(), "protocol_version") {
		t.Fatalf("install = %v; want ErrSource naming protocol_version", err)
	}
}
