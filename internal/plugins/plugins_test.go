package plugins

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/testdb"
	"github.com/davidgibbons/innkeeper/protocol"
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

func TestInstance(t *testing.T) {
	ctx := context.Background()
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	m := &protocol.Manifest{Name: "fake", Version: "0.0.1"}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{"fake": m}, store)

	if _, err := reg.CreateInstance(ctx, "missing", "x", nil); !errors.Is(err, ErrUnknownPlugin) {
		t.Fatalf("create for missing plugin: %v", err)
	}
	id, err := reg.CreateInstance(ctx, "fake", "Fake", map[string]any{"path": "/cards"})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Manifest != m || inst.Params.Config["path"] != "/cards" || inst.Params.BlobTmp != store.TmpDir() {
		t.Fatalf("instance = %+v", inst)
	}
	if _, err := os.Stat(store.TmpDir()); err != nil {
		t.Fatalf("blob tmp folder: %v", err)
	}

	if err := reg.MarkBroken(ctx, id, errors.New("wrong protocol")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Instance(ctx, id); !errors.Is(err, ErrBroken) {
		t.Fatalf("broken instance: %v", err)
	}
	if _, err := reg.Instance(ctx, id+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing instance: %v", err)
	}
}
