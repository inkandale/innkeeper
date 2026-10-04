// Package plugins loads installed plugins' manifests and the configured
// instances that run them.
package plugins

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

var (
	ErrNotFound      = errors.New("plugin instance not found")
	ErrUnknownPlugin = errors.New("plugin is not installed")
	// ErrBroken means the instance's plugin failed to start. It stays broken
	// until the owner changes the instance.
	ErrBroken = errors.New("plugin instance is broken")
)

// LoadDir loads the manifest in each folder of dir. A folder with an invalid
// manifest is logged and left out, so one bad plugin doesn't stop the rest.
func LoadDir(dir string, log *slog.Logger) (map[string]*protocol.Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]*protocol.Manifest{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := protocol.LoadManifest(filepath.Join(dir, e.Name()))
		if err != nil {
			log.Error("skip plugin", "dir", e.Name(), "err", err)
			continue
		}
		if _, dup := out[m.Name]; dup {
			log.Error("skip plugin: name already loaded", "dir", e.Name(), "plugin", m.Name)
			continue
		}
		out[m.Name] = m
	}
	return out, nil
}

// Registry resolves plugin instances to what the plugin host needs to run
// them.
type Registry struct {
	pool      *pgxpool.Pool
	manifests map[string]*protocol.Manifest
	blobs     *blobstore.Store
}

// New returns a Registry for the installed plugins in manifests, whose
// plugins write new blobs to blobs.
func New(pool *pgxpool.Pool, manifests map[string]*protocol.Manifest, blobs *blobstore.Store) *Registry {
	return &Registry{pool: pool, manifests: manifests, blobs: blobs}
}

// CreateInstance configures a new instance of an installed plugin.
func (r *Registry) CreateInstance(ctx context.Context, plugin, name string, config map[string]any) (int64, error) {
	if _, ok := r.manifests[plugin]; !ok {
		return 0, fmt.Errorf("%w: %s", ErrUnknownPlugin, plugin)
	}
	if config == nil {
		config = map[string]any{}
	}
	var id int64
	err := r.pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name, config) VALUES ($1, $2, $3) RETURNING id`,
		plugin, name, config).Scan(&id)
	return id, err
}

// Instance returns what the plugin host needs to run an instance.
func (r *Registry) Instance(ctx context.Context, id int64) (pluginhost.Instance, error) {
	var plugin string
	var config map[string]any
	var broken *string
	err := r.pool.QueryRow(ctx, `SELECT plugin, config, broken_error FROM plugin_instance WHERE id = $1`, id).
		Scan(&plugin, &config, &broken)
	if errors.Is(err, pgx.ErrNoRows) {
		return pluginhost.Instance{}, ErrNotFound
	}
	if err != nil {
		return pluginhost.Instance{}, err
	}
	if broken != nil {
		return pluginhost.Instance{}, fmt.Errorf("%w: %s", ErrBroken, *broken)
	}
	m, ok := r.manifests[plugin]
	if !ok {
		return pluginhost.Instance{}, fmt.Errorf("%w: %s", ErrUnknownPlugin, plugin)
	}
	if err := os.MkdirAll(r.blobs.TmpDir(), 0o755); err != nil {
		return pluginhost.Instance{}, err
	}
	// ponytail: no secrets or plugin database role yet; plan 4 adds
	// encrypted secrets, and the archive catalog needs the role.
	return pluginhost.Instance{
		ID:       strconv.FormatInt(id, 10),
		Manifest: m,
		Params: protocol.InitializeParams{
			Config:     config,
			BlobStores: map[string]string{r.blobs.Name: r.blobs.Root},
			BlobTmp:    r.blobs.TmpDir(),
		},
	}, nil
}

// MarkBroken records that an instance's plugin failed to start, so nothing
// starts it again until the owner changes the instance.
func (r *Registry) MarkBroken(ctx context.Context, id int64, cause error) error {
	_, err := r.pool.Exec(ctx, `UPDATE plugin_instance SET broken_error = $2, broken_at = now() WHERE id = $1`,
		id, cause.Error())
	return err
}
