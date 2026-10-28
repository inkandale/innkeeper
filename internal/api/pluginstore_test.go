package api

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testutil"
)

func TestPluginStore(t *testing.T) {
	e := newEnv(t)
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := plugins.Open(context.Background(), e.pool, plugins.Dirs{Installed: t.TempDir()},
		&blobstore.Store{Name: "library", Root: t.TempDir()}, box, "", quiet)
	if err != nil {
		t.Fatal(err)
	}
	e.s.Registry = reg
	src := testutil.PluginSource(t,
		testutil.PluginRelease{Name: "good", Version: "1.0.0"},
		testutil.PluginRelease{Name: "good", Version: "2.0.0", Notes: "- Push lorebooks"})

	e.call(http.MethodPost, "/plugin-sources", map[string]string{"id": "local", "url": src}, http.StatusCreated, nil)
	e.call(http.MethodPost, "/plugin-sources", map[string]string{"id": "web", "url": "http://example.com/i.toml"}, http.StatusBadRequest, nil)

	var catalog []catalogSourceOut
	e.call(http.MethodGet, "/plugin-catalog", nil, http.StatusOK, &catalog)
	if len(catalog) != 1 || len(catalog[0].Plugins) != 1 || catalog[0].Plugins[0].Version != "2.0.0" ||
		len(catalog[0].Plugins[0].Versions) != 2 || catalog[0].Plugins[0].Versions[1] != "1.0.0" ||
		len(catalog[0].Plugins[0].Notes) != 1 || catalog[0].Plugins[0].Notes["2.0.0"] != "- Push lorebooks" {
		t.Fatalf("catalog = %+v", catalog)
	}

	e.call(http.MethodPost, "/plugins/good/install", map[string]string{"source": "local", "version": "1.0.0"}, http.StatusNoContent, nil)
	e.call(http.MethodPost, "/plugins/good/install", map[string]string{"source": "local", "version": "2.0.0"}, http.StatusNoContent, nil)
	var list []pluginOut
	e.call(http.MethodGet, "/plugins", nil, http.StatusOK, &list)
	if len(list) != 1 || list[0].Source != "local" || list[0].Version != "2.0.0" ||
		list[0].PreviousVersion == nil || *list[0].PreviousVersion != "1.0.0" {
		t.Fatalf("plugins = %+v", list)
	}
	if list[0].Status != "ready" || list[0].MultipleInstances {
		t.Fatalf("status = %q, multiple = %v; want ready, false", list[0].Status, list[0].MultipleInstances)
	}
	var instances []plugins.InstanceInfo
	e.call(http.MethodGet, "/instances", nil, http.StatusOK, &instances)
	ours := slices.DeleteFunc(instances, func(i plugins.InstanceInfo) bool { return i.Plugin != "good" })
	if len(ours) != 1 {
		t.Fatalf("good instances = %+v; want the one install created", ours)
	}
	e.call(http.MethodPost, "/instances", map[string]string{"plugin": "good", "name": "second"}, http.StatusConflict, nil)

	e.call(http.MethodDelete, "/plugin-sources/local", nil, http.StatusConflict, nil)
	e.call(http.MethodDelete, "/plugins/good/data", nil, http.StatusBadRequest, nil)
	e.call(http.MethodDelete, "/plugins/good", nil, http.StatusNoContent, nil)
	e.call(http.MethodDelete, "/plugins/good/data", nil, http.StatusNoContent, nil)
	e.call(http.MethodDelete, "/plugins/good", nil, http.StatusNotFound, nil)
}

func TestPluginNeedsWhatItUsesOffer(t *testing.T) {
	e := newEnv(t)
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := plugins.Open(context.Background(), e.pool, plugins.Dirs{Installed: t.TempDir()},
		&blobstore.Store{Name: "library", Root: t.TempDir()}, box, "", quiet)
	if err != nil {
		t.Fatal(err)
	}
	e.s.Registry = reg
	src := testutil.PluginSource(t, testutil.PluginRelease{Name: "writer", Version: "1.0.0", Uses: []string{"ai.chat", "ai.decide"}})
	e.call(http.MethodPost, "/plugin-sources", map[string]string{"id": "local", "url": src}, http.StatusCreated, nil)

	var catalog []catalogSourceOut
	e.call(http.MethodGet, "/plugin-catalog", nil, http.StatusOK, &catalog)
	if got := catalog[0].Plugins[0].Needs; !slices.Equal(got, []string{"ai"}) {
		t.Fatalf("catalog needs = %v; want [ai]", got)
	}
	e.call(http.MethodPost, "/plugins/writer/install", map[string]string{"source": "local", "version": "1.0.0"}, http.StatusNoContent, nil)
	var list []pluginOut
	e.call(http.MethodGet, "/plugins", nil, http.StatusOK, &list)
	if len(list) != 1 || !slices.Equal(list[0].Needs, []string{"ai"}) {
		t.Fatalf("plugins = %+v; want writer needing [ai]", list)
	}
}
