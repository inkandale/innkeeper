package plugins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

const fakePage = `{"source": ["https://fake.example/x/y"]}`

// sitesRegistry has two fake sites plugins, a and b, and no instances.
func sitesRegistry(t *testing.T) (*Registry, *pluginhost.Manager) {
	t.Helper()
	manifests := map[string]*protocol.Manifest{}
	for _, name := range []string{"a", "b"} {
		m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"sites"}, "-name="+name))
		if err != nil {
			t.Fatal(err)
		}
		m.Name = name
		manifests[name] = m
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := New(testdb.New(t), manifests, &blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	reg.log = quiet
	mgr := pluginhost.NewManager(nil, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	return reg, mgr
}

func addSites(t *testing.T, reg *Registry, plugin, name string, config map[string]any) int64 {
	t.Helper()
	id, err := reg.CreateInstance(context.Background(), NewInstance{Plugin: plugin, Name: name, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func tags(srcs []library.Source) string {
	var out []string
	for _, s := range srcs {
		out = append(out, s.Kind+":"+s.Ref+":"+s.ExternalID)
	}
	return strings.Join(out, ",")
}

func TestDetectSourcesOrdersByInstanceAndFirstWins(t *testing.T) {
	ctx := context.Background()
	reg, mgr := sitesRegistry(t)
	addSites(t, reg, "b", "B", map[string]any{"sites_tag": "B"})
	addSites(t, reg, "a", "A", map[string]any{"sites_tag": "A"})
	// Only a plugin's oldest instance is asked.
	addSites(t, reg, "a", "A2", map[string]any{"sites_tag": "A2", "sites_fail": "fatal"})
	got, err := reg.DetectSources(ctx, mgr, library.Card, []byte(fakePage))
	if err != nil {
		t.Fatal(err)
	}
	if tags(got) != "fake:x/y:B" || got[0].URL != "https://fake.example/x/y" {
		t.Fatalf("got %+v, want b's source only", got)
	}
}

func TestDetectSourcesSkipsFailingPlugins(t *testing.T) {
	ctx := context.Background()
	sitesTimeout = 300 * time.Millisecond
	t.Cleanup(func() { sitesTimeout = 5 * time.Second })
	reg, mgr := sitesRegistry(t)
	tests := []struct {
		name   string
		config map[string]any
		retry  bool
	}{
		{"erroring", map[string]any{"sites_fail": "fatal"}, false},
		{"retryable", map[string]any{"sites_fail": "retryable"}, true},
		{"hanging", map[string]any{"sites_hang": true}, false},
		{"invalid source", map[string]any{"sites_bad": true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.config["sites_tag"] = "A"
			a := addSites(t, reg, "a", "A "+tt.name, tt.config)
			b := addSites(t, reg, "b", "B "+tt.name, map[string]any{"sites_tag": "B"})
			t.Cleanup(func() {
				_ = reg.DeleteInstance(ctx, a)
				_ = reg.DeleteInstance(ctx, b)
			})
			got, err := reg.DetectSources(ctx, mgr, library.Card, []byte(fakePage))
			if (err != nil) != tt.retry || tt.retry && !Retryable(err) {
				t.Fatalf("err = %v, want retryable %v", err, tt.retry)
			}
			if tags(got) != "fake:x/y:B" {
				t.Fatalf("got %s, want only b's source", tags(got))
			}
		})
	}
}

func TestDetectSourcesSkipsBrokenInstance(t *testing.T) {
	ctx := context.Background()
	reg, mgr := sitesRegistry(t)
	old := addSites(t, reg, "a", "A", map[string]any{"sites_tag": "A"})
	addSites(t, reg, "a", "A2", map[string]any{"sites_tag": "A2"})
	inst, err := reg.Instance(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.MarkBroken(ctx, old, inst.Version, errors.New("won't start")); err != nil {
		t.Fatal(err)
	}
	got, err := reg.DetectSources(ctx, mgr, library.Lorebook, []byte(fakePage))
	if err != nil || tags(got) != "fake:x/y:A2" {
		t.Fatalf("got %s, %v; want the next working instance's source", tags(got), err)
	}
}

func TestDetectSourcesWithoutSitesPlugin(t *testing.T) {
	reg, mgr := sitesRegistry(t)
	got, err := reg.DetectSources(context.Background(), mgr, library.Card,
		[]byte(`{"extensions": {"chub": {"id": 7, "full_path": "u/c"}}}`))
	if !errors.Is(err, ErrNoSites) || len(got) != 0 {
		t.Fatalf("got %s, %v; want nothing and ErrNoSites", tags(got), err)
	}
}

func TestParseSource(t *testing.T) {
	ctx := context.Background()
	reg, mgr := sitesRegistry(t)

	_, err := reg.ParseSource(ctx, mgr, library.Card, library.Source{URL: "https://chub.ai/characters/u/c"})
	if !errors.Is(err, library.ErrInvalid) || !strings.Contains(err.Error(), "no installed plugin recognizes that URL") {
		t.Fatalf("without a sites plugin: %v, want the 422 wording", err)
	}

	addSites(t, reg, "a", "A", map[string]any{"sites_tag": "A"})
	addSites(t, reg, "b", "B", map[string]any{"sites_bad": true})
	got, err := reg.ParseSource(ctx, mgr, library.Card, library.Source{URL: " https://fake.example/x/y "})
	if err != nil || tags([]library.Source{got}) != "fake:x/y:A" {
		t.Fatalf("by url: %+v, %v", got, err)
	}
	got, err = reg.ParseSource(ctx, mgr, library.Lorebook, library.Source{Kind: "fake", Ref: "x/y", ExternalID: "mine"})
	if err != nil || got.ExternalID != "mine" || got.URL != "https://fake.example/x/y" {
		t.Fatalf("by kind and ref: %+v, %v; want the owner's external_id kept", got, err)
	}

	errTests := []struct {
		name string
		in   library.Source
		is   error
		want string
	}{
		{"foreign url", library.Source{URL: "https://example.com/x/y"}, library.ErrInvalid, "no installed plugin recognizes that URL"},
		{"unknown kind", library.Source{Kind: "chub", Ref: "u/c"}, library.ErrInvalid, `no installed plugin knows source kind "chub"`},
		{"bad ref", library.Source{Kind: "fake", Ref: "x"}, library.ErrInvalid, "a fake ref is <a>/<b>"},
	}
	for _, tt := range errTests {
		if _, err := reg.ParseSource(ctx, mgr, library.Card, tt.in); !errors.Is(err, tt.is) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want %q", tt.name, err, tt.want)
		}
	}

	// a claims the URL first, so b's invalid source is never seen; alone, b's is a bad reply.
	a, err := reg.InstancesWith(ctx, "sites")
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.DeleteInstance(ctx, a[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.ParseSource(ctx, mgr, library.Card, library.Source{URL: "https://fake.example/x/y"}); !errors.Is(err, ErrBadSource) || !strings.Contains(err.Error(), "b:") {
		t.Fatalf("invalid source: %v, want ErrBadSource naming b", err)
	}
}

func TestEnsureSingletons(t *testing.T) {
	ctx := context.Background()
	dirs := Dirs{Builtin: t.TempDir(), Installed: t.TempDir()}
	if err := os.Rename(testutil.FakePluginWith(t, []string{"sites"}), filepath.Join(dirs.Builtin, "fake")); err != nil {
		t.Fatal(err)
	}
	// The fake allows many instances; this one is a singleton with no setup.
	head := "version = \"1\"\nprotocol = 1\ncommand = [\"./x\"]\n"
	writeManifest(t, filepath.Join(dirs.Builtin, "solo"), "name = \"solo\"\n"+head+"capabilities = [\"codec\"]\nformats = [\"png\"]\n")
	writeManifest(t, filepath.Join(dirs.Builtin, "keyed"), "name = \"keyed\"\n"+head+"capabilities = [\"import\"]\nsecrets = [\"key\"]\n")
	writeManifest(t, filepath.Join(dirs.Builtin, "configured"), "name = \"configured\"\n"+head+"capabilities = [\"import\"]\n"+
		"[config]\ntype = \"object\"\nrequired = [\"url\"]\n")
	writeManifest(t, filepath.Join(dirs.Builtin, "login"), "name = \"login\"\n"+head+"capabilities = [\"auth\"]\n")
	writeManifest(t, filepath.Join(dirs.Builtin, "many"), "name = \"many\"\n"+head+"capabilities = [\"import\"]\nmultiple_instances = true\n")
	pool := testdb.New(t)
	api, worker := openWith(t, pool, dirs), openWith(t, pool, dirs)

	// The api and the worker start at once.
	created := make(chan []string, 2)
	for _, reg := range []*Registry{api, worker} {
		go func() {
			names, err := reg.EnsureSingletons(ctx)
			if err != nil {
				t.Error(err)
			}
			created <- names
		}()
	}
	if got := slices.Sorted(slices.Values(append(<-created, <-created...))); !slices.Equal(got, []string{"solo"}) {
		t.Fatalf("created %v, want [solo]", got)
	}
	list, err := api.ListInstances(ctx)
	if err != nil || len(list) != 1 || list[0].Plugin != "solo" {
		t.Fatalf("instances = %+v, %v; want one of solo only", list, err)
	}

	if err := api.DeleteInstance(ctx, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if names, err := worker.EnsureSingletons(ctx); err != nil || len(names) != 0 {
		t.Fatalf("after the owner deleted it: created %v, %v; want it to stay deleted", names, err)
	}
}
