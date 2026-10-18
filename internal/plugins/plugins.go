// Package plugins loads installed plugins' manifests and the configured
// instances that run them.
package plugins

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/db"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/protocol"
)

var (
	ErrNotFound      = errors.New("plugin instance not found")
	ErrUnknownPlugin = errors.New("plugin is not installed")
	// ErrBroken means the instance's plugin failed to start. It stays broken
	// until the owner changes the instance or a check starts it.
	ErrBroken = errors.New("plugin instance is broken")
	// ErrLastLogin means disabling the instance would leave no way to log in.
	ErrLastLogin = errors.New("this is the last working login provider; add another before disabling it")
	// ErrDisabled means the owner disabled the instance; nothing uses it until they enable it.
	ErrDisabled  = errors.New("plugin instance is disabled")
	ErrInvalid   = errors.New("invalid plugin instance")
	ErrNameTaken = errors.New("plugin instance name already in use")
	// ErrNeedsName means the plugin already has an unnamed instance.
	ErrNeedsName = errors.New("plugin already has an instance without a name; name this one")
	// ErrInUse means cards are still bound to the instance.
	ErrInUse = errors.New("plugin instance has bound cards; unbind them first")
	// ErrIncapable means the instance's plugin lacks the capability asked for.
	ErrIncapable = errors.New("plugin lacks the capability")
	// ErrNoCodec means no working codec instance writes a format.
	ErrNoCodec = errors.New("no codec plugin instance writes this format")
	// ErrSingleton means the plugin allows one instance and already has it.
	ErrSingleton = errors.New("plugin allows one instance and already has it")
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

// LoadInstalled loads the plugins the owner installed into dir. Each plugin's
// folder holds one folder per version and a current symlink to one of them.
// A missing dir has no plugins.
func LoadInstalled(dir string, log *slog.Logger) (map[string]*protocol.Manifest, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]*protocol.Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]*protocol.Manifest{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Resolved, so a running process keeps its own version's folder after current moves.
		cur, err := filepath.EvalSymlinks(filepath.Join(dir, e.Name(), "current"))
		if err != nil {
			log.Error("skip plugin: no current version", "dir", e.Name(), "err", err)
			continue
		}
		m, err := protocol.LoadManifest(cur)
		if err == nil && m.Name != e.Name() {
			err = fmt.Errorf("manifest names %q", m.Name)
		}
		if err != nil {
			log.Error("skip plugin", "dir", e.Name(), "err", err)
			continue
		}
		out[m.Name] = m
	}
	return out, nil
}

// Registry resolves plugin instances to what the plugin host needs to run
// them.
type Registry struct {
	pool   *pgxpool.Pool
	blobs  *blobstore.Store
	box    *secrets.Box
	dirs   Dirs
	dbURL  string
	log    *slog.Logger
	client *http.Client

	// mu guards the fields below, which a reload replaces.
	mu        sync.RWMutex
	manifests map[string]*protocol.Manifest
	builtin   map[string]*protocol.Manifest
	dsns      map[string]string // by plugin, set by ProvisionDatabases
	gen       int64             // plugin_generation at the last load; -1 before it

	// installMu serializes installs, uninstalls, and data removal.
	installMu sync.Mutex
}

// Dirs are where a Registry finds plugins.
type Dirs struct {
	// Builtin holds the image's plugins, one folder each, or "" for none.
	Builtin string
	// Installed holds the plugins the owner installs.
	Installed string
}

// New returns a Registry for a fixed set of plugins, for tests and tools that
// don't install. blobs is where plugins write new blobs; box seals instance
// secrets.
func New(pool *pgxpool.Pool, manifests map[string]*protocol.Manifest, blobs *blobstore.Store, box *secrets.Box) *Registry {
	return &Registry{pool: pool, manifests: manifests, blobs: blobs, box: box, gen: -1, log: slog.Default(), client: &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return errors.New("redirect leaves https")
			}
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}}
}

// Open loads the plugins in dirs and provisions their databases. dbURL is the
// core's database URL, which provisioning needs.
func Open(ctx context.Context, pool *pgxpool.Pool, dirs Dirs, blobs *blobstore.Store, box *secrets.Box, dbURL string, log *slog.Logger) (*Registry, error) {
	if err := os.MkdirAll(dirs.Installed, 0o755); err != nil {
		return nil, err
	}
	r := New(pool, nil, blobs, box)
	r.dirs, r.dbURL, r.log = dirs, dbURL, log
	return r, r.Sync(ctx)
}

// Sync reloads the plugins from disk when an install or uninstall happened
// since the last load, in this process or another.
func (r *Registry) Sync(ctx context.Context) error {
	var gen int64
	err := r.pool.QueryRow(ctx, `SELECT CASE WHEN is_called THEN last_value ELSE 0 END FROM plugin_generation`).Scan(&gen)
	if err != nil {
		return err
	}
	r.mu.RLock()
	same := gen == r.gen
	r.mu.RUnlock()
	if same {
		return nil
	}
	builtin := map[string]*protocol.Manifest{}
	if r.dirs.Builtin != "" {
		if builtin, err = LoadDir(r.dirs.Builtin, r.log); err != nil {
			return err
		}
	}
	installed, err := LoadInstalled(r.dirs.Installed, r.log)
	if err != nil {
		return err
	}
	all := maps.Clone(builtin)
	maps.Copy(all, installed)
	r.mu.Lock()
	r.manifests, r.builtin, r.gen = all, builtin, gen
	r.mu.Unlock()
	return r.ProvisionDatabases(ctx, r.dbURL)
}

func (r *Registry) manifest(name string) (*protocol.Manifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.manifests[name]
	return m, ok
}

func (r *Registry) dsn(plugin string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dsns[plugin]
}

// IsBuiltin reports whether the image ships plugin name, so uninstalling it
// falls back to that copy.
func (r *Registry) IsBuiltin(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.builtin[name]
	return ok
}

// HasCapability reports whether plugin is installed and declares capability.
func (r *Registry) HasCapability(plugin, capability string) bool {
	m, ok := r.manifest(plugin)
	return ok && m.Has(capability)
}

// pluginsWith returns the names of the installed plugins that declare capability.
func (r *Registry) pluginsWith(capability string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var names []string
	for name, m := range r.manifests {
		if m.Has(capability) {
			names = append(names, name)
		}
	}
	return names
}

// Manifests returns the installed plugins by name.
func (r *Registry) Manifests() map[string]*protocol.Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return maps.Clone(r.manifests)
}

// InstanceInfo is an instance as the owner sees it. It names the secrets
// that are set but never holds their values.
type InstanceInfo struct {
	ID     int64  `json:"id"`
	Plugin string `json:"plugin"`
	// Name tells apart instances of one plugin; without one, the plugin's name stands in.
	Name *string `json:"name,omitempty"`
	// Label is what to show for the instance: its name, or its plugin's.
	Label        string         `json:"label"`
	Config       map[string]any `json:"config"`
	Secrets      []string       `json:"secrets"`
	OwnerSubject *string        `json:"owner_subject,omitempty"`
	// LibraryAccess lets an action plugin read the library with library.call.
	LibraryAccess bool       `json:"library_access"`
	BrokenError   *string    `json:"broken_error,omitempty"`
	BrokenAt      *time.Time `json:"broken_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Disabled      bool       `json:"disabled"`
	// Status is disabled, broken, needs_setup, or ready, in that order.
	Status string `json:"status" enum:"ready,needs_setup,broken,disabled"`
}

// Usable reports whether anything may use the instance.
func (i InstanceInfo) Usable() bool { return i.BrokenError == nil && !i.Disabled }

// Status sums up a plugin's instances for the Plugins page, in the same words
// as an instance's status: a broken or unset instance shows first, and a
// plugin is disabled only when every instance is.
func Status(m *protocol.Manifest, instances []InstanceInfo) string {
	has := func(s string) bool {
		return slices.ContainsFunc(instances, func(i InstanceInfo) bool { return i.Status == s })
	}
	switch {
	case m.IsTheme():
		return "ready"
	case len(instances) == 0:
		return "needs_setup"
	case has("broken"):
		return "broken"
	case has("needs_setup"):
		return "needs_setup"
	case !has("ready"):
		return "disabled"
	}
	return "ready"
}

// NewInstance is a new instance's settings.
type NewInstance struct {
	Plugin        string
	Name          *string
	Config        map[string]any
	Secrets       map[string]string
	OwnerSubject  *string
	LibraryAccess bool
	// DefaultBases maps a JSON pointer the owner just overrode to the default it replaced.
	DefaultBases map[string]any
}

// InstanceEdit changes an instance. Nil fields stay as they are. A nil value
// in Secrets removes that secret, and an empty Name or OwnerSubject clears it.
type InstanceEdit struct {
	Name          *string
	Config        map[string]any
	Secrets       map[string]*string
	OwnerSubject  *string
	LibraryAccess *bool
	Disabled      *bool
	// DefaultBases maps a JSON pointer the owner just overrode to the default it replaced.
	DefaultBases map[string]any
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
	case errors.As(err, &pgErr) && pgErr.ConstraintName == "plugin_instance_unnamed_key":
		return ErrNeedsName
	case db.IsCode(err, db.UniqueViolation):
		return ErrNameTaken
	case errors.As(err, &pgErr) && pgErr.Code == db.CheckViolation:
		return fmt.Errorf("%w: %s", ErrInvalid, pgErr.ConstraintName)
	}
	return err
}

// lockPlugin holds a lock on plugin's instances until tx ends, so a check
// for an existing instance stays true until the insert.
func lockPlugin(ctx context.Context, tx pgx.Tx, plugin string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('plugin_instance:' || $1))`, plugin)
	return err
}

// CreateInstance configures a new instance of an installed plugin.
func (r *Registry) CreateInstance(ctx context.Context, n NewInstance) (int64, error) {
	m, ok := r.manifest(n.Plugin)
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
	if err := checkBases(n.DefaultBases); err != nil {
		return 0, err
	}
	sealed, err := r.box.Seal(n.Secrets)
	if err != nil {
		return 0, err
	}
	if n.Config == nil {
		n.Config = map[string]any{}
	}
	hashes := mergeDefaultHashes(nil, n.Config, n.DefaultBases)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if err := lockPlugin(ctx, tx, n.Plugin); err != nil {
		return 0, err
	}
	if !m.AllowsManyInstances() {
		n.Name = nil
		var exists bool
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM plugin_instance WHERE plugin = $1)`, n.Plugin).Scan(&exists)
		if err != nil {
			return 0, err
		}
		if exists {
			return 0, fmt.Errorf("%w: %s", ErrSingleton, n.Plugin)
		}
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name, config, secrets, owner_subject, library_access, default_hashes)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, n.Plugin, n.Name, n.Config, sealed, n.OwnerSubject, n.LibraryAccess, hashes).Scan(&id)
	if err != nil {
		return 0, writeErr(err)
	}
	return id, tx.Commit(ctx)
}

// UpdateInstance applies e and clears the instance's broken state, so the
// next call starts its plugin with the new settings. Changing the owner,
// config, or secrets, or disabling it, also ends the instance's login
// sessions. It refuses to disable the last working login provider.
func (r *Registry) UpdateInstance(ctx context.Context, id int64, e InstanceEdit) error {
	if err := checkBases(e.DefaultBases); err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var plugin string
	var sealed []byte
	var hashes map[string]string
	var usable bool
	err = tx.QueryRow(ctx, `SELECT plugin, secrets, default_hashes, owner_subject IS NOT NULL AND broken_error IS NULL AND NOT disabled
		FROM plugin_instance WHERE id = $1 FOR UPDATE`, id).Scan(&plugin, &sealed, &hashes, &usable)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if e.Disabled != nil && *e.Disabled && r.HasCapability(plugin, "auth") && usable {
		// One lock for every login disable, so two of them can't each see the other as the survivor.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('login-providers'))`); err != nil {
			return err
		}
		var others int
		err := tx.QueryRow(ctx, `SELECT count(*) FROM plugin_instance
			WHERE id <> $1 AND plugin = ANY($2) AND owner_subject IS NOT NULL AND broken_error IS NULL AND NOT disabled`,
			id, r.pluginsWith("auth")).Scan(&others)
		if err != nil {
			return err
		}
		if others == 0 {
			return ErrLastLogin
		}
	}
	if e.Secrets != nil {
		m, ok := r.manifest(plugin)
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
	// An untyped nil sends SQL NULL, which keeps the current config and hashes.
	var config, newHashes any
	if e.Config != nil {
		config, newHashes = e.Config, mergeDefaultHashes(hashes, e.Config, e.DefaultBases)
	}
	owner := ""
	if e.OwnerSubject != nil {
		owner = *e.OwnerSubject
	}
	// A singleton's one instance has nothing to be told apart from.
	m, ok := r.manifest(plugin)
	single := ok && !m.AllowsManyInstances()
	_, err = tx.Exec(ctx, `UPDATE plugin_instance SET
			name = CASE WHEN $10 THEN NULL WHEN $2::text IS NULL THEN name ELSE nullif($2, '') END, config = coalesce($3, config),
			secrets = $4, owner_subject = CASE WHEN $5 THEN nullif($6::text, '') ELSE owner_subject END,
			library_access = coalesce($7, library_access), default_hashes = coalesce($8, default_hashes),
			disabled = coalesce($9, disabled), broken_error = NULL, broken_at = NULL, updated_at = now()
		WHERE id = $1`, id, e.Name, config, sealed, e.OwnerSubject != nil, owner, e.LibraryAccess, newHashes, e.Disabled, single)
	if err != nil {
		return writeErr(err)
	}
	if e.OwnerSubject != nil || e.Config != nil || e.Secrets != nil || e.Disabled != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM session WHERE instance_id = $1`, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// DeleteInstance removes an instance. Its jobs stay, without an instance,
// and logins through it end. It returns ErrInUse while cards are bound to it.
func (r *Registry) DeleteInstance(ctx context.Context, id int64) error {
	n, err := deleteInstances(ctx, r.pool, "id", id)
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// deleteInstances deletes the instances whose column (id or plugin) is
// value, and how many it deleted. Their import bindings go with them, since
// those only record what an importer last sent; any other binding makes it
// fail with ErrInUse.
func deleteInstances(ctx context.Context, db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, column string, value any) (int64, error) {
	tag, err := db.Exec(ctx, `WITH b AS (DELETE FROM binding WHERE imported
			AND instance_id IN (SELECT id FROM plugin_instance WHERE `+column+` = $1))
		DELETE FROM plugin_instance WHERE `+column+` = $1`, value)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "binding_instance_id_fkey" {
		return 0, ErrInUse
	}
	return tag.RowsAffected(), err
}

const infoCols = `id, plugin, name, config, secrets, owner_subject, library_access, broken_error, broken_at,
	created_at, updated_at, disabled`

func (r *Registry) scanInfo(row pgx.Row) (InstanceInfo, error) {
	var i InstanceInfo
	var sealed []byte
	err := row.Scan(&i.ID, &i.Plugin, &i.Name, &i.Config, &sealed, &i.OwnerSubject, &i.LibraryAccess,
		&i.BrokenError, &i.BrokenAt, &i.CreatedAt, &i.UpdatedAt, &i.Disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, ErrNotFound
	}
	if err != nil {
		return i, err
	}
	i.Label = i.Plugin
	if i.Name != nil {
		i.Label = *i.Name
	}
	s, err := r.box.Open(sealed)
	if err != nil {
		return i, err
	}
	// Start non-nil so JSON shows [] rather than null.
	i.Secrets = slices.AppendSeq([]string{}, maps.Keys(s))
	slices.Sort(i.Secrets)
	i.Status = r.instanceStatus(i)
	return i, nil
}

func (r *Registry) instanceStatus(i InstanceInfo) string {
	switch {
	case i.Disabled:
		return "disabled"
	case i.BrokenError != nil:
		return "broken"
	}
	m, ok := r.manifest(i.Plugin)
	if !ok {
		return "broken"
	}
	required, _ := m.Config["required"].([]any)
	for _, k := range required {
		if key, _ := k.(string); key != "" && i.Config[key] == nil {
			return "needs_setup"
		}
	}
	return "ready"
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

// InstancesWith returns the instances whose plugins declare every one of
// capabilities, oldest first.
func (r *Registry) InstancesWith(ctx context.Context, capabilities ...string) ([]InstanceInfo, error) {
	list, err := r.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(list, func(i InstanceInfo) bool {
		return slices.ContainsFunc(capabilities, func(c string) bool { return !r.HasCapability(i.Plugin, c) })
	}), nil
}

// InstanceWith is Instance for an instance whose plugin declares capability.
func (r *Registry) InstanceWith(ctx context.Context, id int64, capability string) (pluginhost.Instance, error) {
	inst, err := r.Instance(ctx, id)
	if err == nil && !inst.Manifest.Has(capability) {
		err = fmt.Errorf("%w: plugin %s can't %s", ErrIncapable, inst.Manifest.Name, capability)
	}
	return inst, err
}

// Instance returns what the plugin host needs to run an instance.
func (r *Registry) Instance(ctx context.Context, id int64) (pluginhost.Instance, error) {
	inst, st, err := r.load(ctx, id)
	switch {
	case err != nil:
		return inst, err
	case st.disabled:
		return pluginhost.Instance{}, fmt.Errorf("%w: %d", ErrDisabled, id)
	case st.broken != nil:
		return pluginhost.Instance{}, fmt.Errorf("%w: %s", ErrBroken, *st.broken)
	}
	return inst, nil
}

// state is what Instance refuses an instance for.
type state struct {
	broken   *string
	disabled bool
}

// load is Instance without the broken and disabled checks.
func (r *Registry) load(ctx context.Context, id int64) (inst pluginhost.Instance, st state, err error) {
	var plugin string
	var config map[string]any
	var sealed []byte
	var updated time.Time
	err = r.pool.QueryRow(ctx, `SELECT pi.plugin, pi.config, pi.secrets, pi.broken_error, pi.disabled,
			greatest(pi.updated_at, coalesce(p.changed_at, pi.updated_at))
		FROM plugin_instance pi LEFT JOIN plugin_install p ON p.name = pi.plugin WHERE pi.id = $1`, id).
		Scan(&plugin, &config, &sealed, &st.broken, &st.disabled, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return inst, st, ErrNotFound
	}
	if err != nil {
		return inst, st, err
	}
	m, ok := r.manifest(plugin)
	if !ok {
		return inst, st, fmt.Errorf("%w: %s", ErrUnknownPlugin, plugin)
	}
	secrets, err := r.box.Open(sealed)
	if err != nil {
		return inst, st, err
	}
	if err := os.MkdirAll(r.blobs.TmpDir(), 0o755); err != nil {
		return inst, st, err
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
			DatabaseURL: r.dsn(plugin),
		},
	}, st, nil
}

// CodecInstance returns the oldest working instance of a codec plugin that
// lists format.
func (r *Registry) CodecInstance(ctx context.Context, format string) (pluginhost.Instance, error) {
	type row struct {
		ID     int64
		Plugin string
	}
	rows, err := r.pool.Query(ctx, `SELECT id, plugin FROM plugin_instance WHERE broken_error IS NULL AND NOT disabled ORDER BY id`)
	if err != nil {
		return pluginhost.Instance{}, err
	}
	all, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return pluginhost.Instance{}, err
	}
	for _, i := range all {
		if m, ok := r.manifest(i.Plugin); ok && m.Has("codec") && slices.Contains(m.Formats, format) {
			return r.Instance(ctx, i.ID)
		}
	}
	return pluginhost.Instance{}, fmt.Errorf("%w: %q; create an instance of a codec plugin that lists it", ErrNoCodec, format)
}

// MarkBroken records that an instance's plugin failed to start, so nothing
// starts it again until the owner changes or checks the instance. version is
// the Instance.Version that failed; a later edit makes the call a no-op.
func (r *Registry) MarkBroken(ctx context.Context, id int64, version string, cause error) error {
	msg := cause.Error()
	return r.setBroken(ctx, id, version, &msg)
}

// setBroken records cause as the instance's broken error, or clears it for
// nil, if the instance is still at version.
func (r *Registry) setBroken(ctx context.Context, id int64, version string, cause *string) error {
	micros, err := strconv.ParseInt(version, 10, 64)
	if err != nil {
		return fmt.Errorf("instance version %q: %w", version, err)
	}
	_, err = r.pool.Exec(ctx, `UPDATE plugin_instance pi
		SET broken_error = $2::text, broken_at = CASE WHEN $2::text IS NULL THEN NULL ELSE now() END
		WHERE id = $1 AND greatest(updated_at,
			coalesce((SELECT changed_at FROM plugin_install WHERE name = pi.plugin), updated_at)) = $3`,
		id, cause, time.UnixMicro(micros))
	return err
}

// Check starts an instance's plugin even when it's broken: a failed start
// marks it broken, and a good one clears that. A pull target then lists the
// app's cards, which tests its address and login. problem says why the
// instance doesn't work; err means the check itself failed.
func (r *Registry) Check(ctx context.Context, mgr *pluginhost.Manager, id int64) (problem, err error) {
	inst, st, err := r.load(ctx, id)
	if errors.Is(err, ErrUnknownPlugin) {
		return err, nil
	}
	if err != nil {
		return nil, err
	}
	if st.disabled {
		return nil, fmt.Errorf("%w: %d", ErrDisabled, id)
	}
	var startErr *pluginhost.StartError
	if err := mgr.Start(ctx, inst); errors.As(err, &startErr) && ctx.Err() == nil {
		return err, r.MarkBroken(context.WithoutCancel(ctx), id, inst.Version, err)
	} else if err != nil {
		return nil, err
	}
	if st.broken != nil {
		if err := r.setBroken(ctx, id, inst.Version, nil); err != nil {
			return nil, err
		}
	}
	if inst.Manifest.Has("pull") {
		var res protocol.TargetListResult
		if err := r.Call(ctx, mgr, inst, protocol.MethodTargetList, nil, &res); err != nil && ctx.Err() == nil {
			return err, nil
		} else if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// Call sends method to inst's plugin through mgr. A plugin that fails to
// start marks the instance broken.
func (r *Registry) Call(ctx context.Context, mgr *pluginhost.Manager, inst pluginhost.Instance, method string, params, result any) error {
	return r.markStartFailure(ctx, inst, mgr.Call(ctx, inst, method, params, result))
}

// markStartFailure marks inst broken when err is a failed start, and
// returns err.
func (r *Registry) markStartFailure(ctx context.Context, inst pluginhost.Instance, err error) error {
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

// Services lists the service methods inst offers, starting its plugin if
// needed.
func (r *Registry) Services(ctx context.Context, mgr *pluginhost.Manager, inst pluginhost.Instance) ([]string, error) {
	info, err := mgr.Info(ctx, inst)
	return info.Services, r.markStartFailure(ctx, inst, err)
}

// InstancesOffering lists the working instances that offer the service
// method, oldest first, starting their plugins to ask. One that fails to
// start is marked broken and left out.
func (r *Registry) InstancesOffering(ctx context.Context, mgr *pluginhost.Manager, method string) ([]InstanceInfo, error) {
	capability, ok := protocol.Services[method]
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a service", ErrInvalid, method)
	}
	list, err := r.InstancesWith(ctx, capability)
	if err != nil {
		return nil, err
	}
	out := []InstanceInfo{}
	for _, info := range list {
		if !info.Usable() {
			continue
		}
		inst, err := r.Instance(ctx, info.ID)
		if errors.Is(err, ErrBroken) || errors.Is(err, ErrDisabled) {
			continue
		}
		if err != nil {
			return nil, err
		}
		services, err := r.Services(ctx, mgr, inst)
		var startErr *pluginhost.StartError
		if errors.As(err, &startErr) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if slices.Contains(services, method) {
			out = append(out, info)
		}
	}
	return out, nil
}

// Retryable reports whether a failed plugin call may work later: the plugin
// exited, or it replied with a retryable error.
func Retryable(err error) bool {
	var rpcErr *protocol.Error
	return errors.Is(err, protocol.ErrClosed) || errors.As(err, &rpcErr) && rpcErr.Retryable()
}

// JobErr is a job's error for a failed plugin call: retried if the call may
// work later. A plugin that failed to start isn't retried; its instance is
// broken until someone fixes it.
func JobErr(err error) error {
	var startErr *pluginhost.StartError
	if !errors.As(err, &startErr) && Retryable(err) {
		return fmt.Errorf("%w: %w", jobs.ErrRetry, err)
	}
	return err
}

// Setting is a core setting's value, or "" when unset.
func (r *Registry) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := r.pool.QueryRow(ctx, `SELECT value FROM setting WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting stores a core setting; "" removes it.
func (r *Registry) SetSetting(ctx context.Context, key, value string) error {
	if value == "" {
		_, err := r.pool.Exec(ctx, `DELETE FROM setting WHERE key = $1`, key)
		return err
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO setting (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
	return err
}

// Theme is the selected theme plugin's name, or "" for the default theme.
func (r *Registry) Theme(ctx context.Context) (string, error) { return r.Setting(ctx, "theme") }

// SetTheme selects an installed theme plugin, or the default theme for "".
func (r *Registry) SetTheme(ctx context.Context, name string) error {
	if name != "" {
		if m, ok := r.manifest(name); !ok || !m.IsTheme() {
			return fmt.Errorf("%w: %q is not an installed theme", ErrInvalid, name)
		}
	}
	return r.SetSetting(ctx, "theme", name)
}
