package api

import (
	"context"
	"net/http"
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
		testutil.PluginRelease{Name: "good", Version: "2.0.0"})

	e.call(http.MethodPost, "/plugin-sources", map[string]string{"id": "local", "url": src}, http.StatusCreated, nil)
	e.call(http.MethodPost, "/plugin-sources", map[string]string{"id": "web", "url": "http://example.com/i.toml"}, http.StatusBadRequest, nil)

	var catalog []catalogSourceOut
	e.call(http.MethodGet, "/plugin-catalog", nil, http.StatusOK, &catalog)
	if len(catalog) != 1 || len(catalog[0].Plugins) != 1 || catalog[0].Plugins[0].Version != "2.0.0" ||
		len(catalog[0].Plugins[0].Versions) != 2 || catalog[0].Plugins[0].Versions[1] != "1.0.0" {
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

	e.call(http.MethodDelete, "/plugin-sources/local", nil, http.StatusConflict, nil)
	e.call(http.MethodDelete, "/plugins/good/data", nil, http.StatusBadRequest, nil)
	e.call(http.MethodDelete, "/plugins/good", nil, http.StatusNoContent, nil)
	e.call(http.MethodDelete, "/plugins/good/data", nil, http.StatusNoContent, nil)
	e.call(http.MethodDelete, "/plugins/good", nil, http.StatusNotFound, nil)
}
