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
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/secrets"
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

func newRegistry(t *testing.T) (*Registry, *blobstore.Store) {
	t.Helper()
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	m := &protocol.Manifest{Name: "fake", Version: "0.0.1", Secrets: []string{"api_key", "region"}}
	return New(testdb.New(t), map[string]*protocol.Manifest{"fake": m}, store, box), store
}

func TestInstance(t *testing.T) {
	ctx := context.Background()
	reg, store := newRegistry(t)

	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "missing", Name: "x"}); !errors.Is(err, ErrUnknownPlugin) {
		t.Fatalf("create for missing plugin: %v", err)
	}
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake",
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

func TestInstanceInfoHidesSecretValues(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	owner := "owner"
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake",
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
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake",
		Secrets: map[string]string{"password": "x"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("undeclared secret: %v", err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: ""}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake"}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake"}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate name: %v", err)
	}
}

func TestUpdateInstance(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake",
		Config: map[string]any{"path": "/a"}, Secrets: map[string]string{"api_key": "old", "region": "eu"}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.MarkBroken(ctx, id, errors.New("bad key")); err != nil {
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
	if after.Params.Secrets["api_key"] != "new" || len(after.Params.Secrets) != 1 || after.Params.Config["path"] != "/a" {
		t.Fatalf("after edit: %+v", after.Params)
	}
	info, err := reg.GetInstance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Renamed" || *info.OwnerSubject != "owner" || info.BrokenError != nil {
		t.Fatalf("info = %+v", info)
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
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake"})
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
