// Package plugins loads installed plugins' manifests and the configured
// instances that run them.
package plugins

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/secrets"
	"github.com/davidgibbons/innkeeper/protocol"
)

var (
	ErrNotFound      = errors.New("plugin instance not found")
	ErrUnknownPlugin = errors.New("plugin is not installed")
	// ErrBroken means the instance's plugin failed to start. It stays broken
	// until the owner changes the instance.
	ErrBroken    = errors.New("plugin instance is broken")
	ErrInvalid   = errors.New("invalid plugin instance")
	ErrNameTaken = errors.New("plugin instance name already in use")
	// ErrInUse means cards are still bound to the instance.
	ErrInUse = errors.New("plugin instance has bound cards; unbind them first")
	// ErrNoCodec means no working codec instance writes a format.
	ErrNoCodec = errors.New("no codec plugin instance writes this format")
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
	box       *secrets.Box
	dsns      map[string]string // by plugin, set by ProvisionDatabases
}

// New returns a Registry for the installed plugins in manifests, whose
// plugins write new blobs to blobs. box seals instance secrets.
func New(pool *pgxpool.Pool, manifests map[string]*protocol.Manifest, blobs *blobstore.Store, box *secrets.Box) *Registry {
	return &Registry{pool: pool, manifests: manifests, blobs: blobs, box: box}
}

// HasCapability reports whether plugin is installed and declares capability.
func (r *Registry) HasCapability(plugin, capability string) bool {
	m, ok := r.manifests[plugin]
	return ok && slices.Contains(m.Capabilities, capability)
}

// Manifests returns the installed plugins by name.
func (r *Registry) Manifests() map[string]*protocol.Manifest { return r.manifests }

// InstanceInfo is an instance as the owner sees it. It names the secrets
// that are set but never holds their values.
type InstanceInfo struct {
	ID           int64          `json:"id"`
	Plugin       string         `json:"plugin"`
	Name         string         `json:"name"`
	Config       map[string]any `json:"config"`
	Secrets      []string       `json:"secrets"`
	OwnerSubject *string        `json:"owner_subject,omitempty"`
	BrokenError  *string        `json:"broken_error,omitempty"`
	BrokenAt     *time.Time     `json:"broken_at,omitempty"`
	// AutoBindExact makes reconcile bind each exact match it finds.
	AutoBindExact bool      `json:"auto_bind_exact"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// NewInstance is a new instance's settings.
type NewInstance struct {
	Plugin       string
	Name         string
	Config       map[string]any
	Secrets      map[string]string
	OwnerSubject *string
}

// InstanceEdit changes an instance. Nil fields stay as they are. A nil value
// in Secrets removes that secret, and an empty OwnerSubject clears it.
type InstanceEdit struct {
	Name          *string
	Config        map[string]any
	Secrets       map[string]*string
	OwnerSubject  *string
	AutoBindExact *bool
}

func checkSecret(m *protocol.Manifest, name string) error {
	if !slices.Contains(m.Secrets, name) {
		return fmt.Errorf("%w: plugin %s has no secret %q", ErrInvalid, m.Name, name)
	}
	return nil
}

// writeErr maps constraint violations on plugin_instance.
func writeErr(err error) error {
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return ErrNameTaken
	case errors.As(err, &pgErr) && pgErr.Code == "23514":
		return fmt.Errorf("%w: %s", ErrInvalid, pgErr.ConstraintName)
	}
	return err
}

// CreateInstance configures a new instance of an installed plugin.
func (r *Registry) CreateInstance(ctx context.Context, n NewInstance) (int64, error) {
	m, ok := r.manifests[n.Plugin]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrUnknownPlugin, n.Plugin)
	}
	if m.IsTheme() {
		return 0, fmt.Errorf("%w: %s is a theme; select it instead of configuring it", ErrInvalid, n.Plugin)
	}
	for name := range n.Secrets {
		if err := checkSecret(m, name); err != nil {
			return 0, err
		}
	}
	sealed, err := r.box.Seal(n.Secrets)
	if err != nil {
		return 0, err
	}
	if n.Config == nil {
		n.Config = map[string]any{}
	}
	var id int64
	err = r.pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name, config, secrets, owner_subject)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, n.Plugin, n.Name, n.Config, sealed, n.OwnerSubject).Scan(&id)
	return id, writeErr(err)
}

// UpdateInstance applies e and clears the instance's broken state, so the
// next call starts its plugin with the new settings. Changing the owner,
// config, or secrets also ends the instance's login sessions.
func (r *Registry) UpdateInstance(ctx context.Context, id int64, e InstanceEdit) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var plugin string
	var sealed []byte
	err = tx.QueryRow(ctx, `SELECT plugin, secrets FROM plugin_instance WHERE id = $1 FOR UPDATE`, id).Scan(&plugin, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if e.Secrets != nil {
		m, ok := r.manifests[plugin]
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownPlugin, plugin)
		}
		current, err := r.box.Open(sealed)
		if err != nil {
			return err
		}
		for name, v := range e.Secrets {
			if v == nil {
				delete(current, name)
				continue
			}
			if err := checkSecret(m, name); err != nil {
				return err
			}
			current[name] = *v
		}
		if sealed, err = r.box.Seal(current); err != nil {
			return err
		}
	}
	// An untyped nil sends SQL NULL, which keeps the current config.
	var config any
	if e.Config != nil {
		config = e.Config
	}
	owner := ""
	if e.OwnerSubject != nil {
		owner = *e.OwnerSubject
	}
	_, err = tx.Exec(ctx, `UPDATE plugin_instance SET name = coalesce($2, name), config = coalesce($3, config),
			secrets = $4, owner_subject = CASE WHEN $5 THEN nullif($6::text, '') ELSE owner_subject END,
			auto_bind_exact = coalesce($7, auto_bind_exact),
			broken_error = NULL, broken_at = NULL, updated_at = now()
		WHERE id = $1`, id, e.Name, config, sealed, e.OwnerSubject != nil, owner, e.AutoBindExact)
	if err != nil {
		return writeErr(err)
	}
	if e.OwnerSubject != nil || e.Config != nil || e.Secrets != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM session WHERE instance_id = $1`, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// DeleteInstance removes an instance. Its jobs stay, without an instance,
// and logins through it end. It returns ErrInUse while cards are bound to it.
func (r *Registry) DeleteInstance(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM plugin_instance WHERE id = $1`, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "binding_instance_id_fkey" {
		return ErrInUse
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const infoCols = `id, plugin, name, config, secrets, owner_subject, broken_error, broken_at, auto_bind_exact, created_at,
	updated_at`

func (r *Registry) scanInfo(row pgx.Row) (InstanceInfo, error) {
	var i InstanceInfo
	var sealed []byte
	err := row.Scan(&i.ID, &i.Plugin, &i.Name, &i.Config, &sealed, &i.OwnerSubject,
		&i.BrokenError, &i.BrokenAt, &i.AutoBindExact, &i.CreatedAt, &i.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, ErrNotFound
	}
	if err != nil {
		return i, err
	}
	s, err := r.box.Open(sealed)
	if err != nil {
		return i, err
	}
	// Start non-nil so JSON shows [] rather than null.
	i.Secrets = slices.AppendSeq([]string{}, maps.Keys(s))
	slices.Sort(i.Secrets)
	return i, nil
}

// GetInstance returns one instance's settings, without secret values.
func (r *Registry) GetInstance(ctx context.Context, id int64) (InstanceInfo, error) {
	return r.scanInfo(r.pool.QueryRow(ctx, `SELECT `+infoCols+` FROM plugin_instance WHERE id = $1`, id))
}

// ListInstances returns every instance, oldest first.
func (r *Registry) ListInstances(ctx context.Context) ([]InstanceInfo, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+infoCols+` FROM plugin_instance ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (InstanceInfo, error) { return r.scanInfo(row) })
}

// Instance returns what the plugin host needs to run an instance.
func (r *Registry) Instance(ctx context.Context, id int64) (pluginhost.Instance, error) {
	var plugin string
	var config map[string]any
	var sealed []byte
	var broken *string
	var updated time.Time
	err := r.pool.QueryRow(ctx, `SELECT plugin, config, secrets, broken_error, updated_at FROM plugin_instance WHERE id = $1`, id).
		Scan(&plugin, &config, &sealed, &broken, &updated)
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
	secrets, err := r.box.Open(sealed)
	if err != nil {
		return pluginhost.Instance{}, err
	}
	if err := os.MkdirAll(r.blobs.TmpDir(), 0o755); err != nil {
		return pluginhost.Instance{}, err
	}
	return pluginhost.Instance{
		ID:       strconv.FormatInt(id, 10),
		Version:  strconv.FormatInt(updated.UnixMicro(), 10),
		Manifest: m,
		Params: protocol.InitializeParams{
			Config:      config,
			Secrets:     secrets,
			BlobStores:  map[string]string{r.blobs.Name: r.blobs.Root},
			BlobTmp:     r.blobs.TmpDir(),
			DatabaseURL: r.dsns[plugin],
		},
	}, nil
}

// CodecInstance returns the oldest working instance of a codec plugin that
// lists format.
func (r *Registry) CodecInstance(ctx context.Context, format string) (pluginhost.Instance, error) {
	type row struct {
		ID     int64
		Plugin string
	}
	rows, err := r.pool.Query(ctx, `SELECT id, plugin FROM plugin_instance WHERE broken_error IS NULL ORDER BY id`)
	if err != nil {
		return pluginhost.Instance{}, err
	}
	all, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return pluginhost.Instance{}, err
	}
	for _, i := range all {
		if m, ok := r.manifests[i.Plugin]; ok && slices.Contains(m.Capabilities, "codec") && slices.Contains(m.Formats, format) {
			return r.Instance(ctx, i.ID)
		}
	}
	return pluginhost.Instance{}, fmt.Errorf("%w: %q; create an instance of a codec plugin that lists it", ErrNoCodec, format)
}

// MarkBroken records that an instance's plugin failed to start, so nothing
// starts it again until the owner changes the instance. version is the
// Instance.Version that failed; a later edit makes the call a no-op.
func (r *Registry) MarkBroken(ctx context.Context, id int64, version string, cause error) error {
	micros, err := strconv.ParseInt(version, 10, 64)
	if err != nil {
		return fmt.Errorf("instance version %q: %w", version, err)
	}
	_, err = r.pool.Exec(ctx, `UPDATE plugin_instance SET broken_error = $2, broken_at = now() WHERE id = $1 AND updated_at = $3`,
		id, cause.Error(), time.UnixMicro(micros))
	return err
}

// Call sends method to inst's plugin through mgr. A plugin that fails to
// start marks the instance broken.
func (r *Registry) Call(ctx context.Context, mgr *pluginhost.Manager, inst pluginhost.Instance, method string, params, result any) error {
	err := mgr.Call(ctx, inst, method, params, result)
	var startErr *pluginhost.StartError
	if errors.As(err, &startErr) && ctx.Err() == nil {
		id, perr := strconv.ParseInt(inst.ID, 10, 64)
		if perr == nil {
			perr = r.MarkBroken(context.WithoutCancel(ctx), id, inst.Version, err)
		}
		return errors.Join(err, perr)
	}
	return err
}

// Retryable reports whether a failed plugin call may work later: the plugin
// exited, or it replied with a retryable error.
func Retryable(err error) bool {
	var rpcErr *protocol.Error
	return errors.Is(err, protocol.ErrClosed) || errors.As(err, &rpcErr) && rpcErr.Retryable()
}

// Theme is the selected theme plugin's name, or "" for the default theme.
func (r *Registry) Theme(ctx context.Context) (string, error) {
	var name string
	err := r.pool.QueryRow(ctx, `SELECT value FROM setting WHERE key = 'theme'`).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// SetTheme selects an installed theme plugin, or the default theme for "".
func (r *Registry) SetTheme(ctx context.Context, name string) error {
	if name == "" {
		_, err := r.pool.Exec(ctx, `DELETE FROM setting WHERE key = 'theme'`)
		return err
	}
	if m, ok := r.manifests[name]; !ok || !m.IsTheme() {
		return fmt.Errorf("%w: %q is not an installed theme", ErrInvalid, name)
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO setting (key, value) VALUES ('theme', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, name)
	return err
}
