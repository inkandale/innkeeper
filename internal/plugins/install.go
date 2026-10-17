package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/internal/db"
	"github.com/inkandale/innkeeper/protocol"
)

// ErrSourceInUse means plugins are still installed from a source.
var ErrSourceInUse = errors.New("plugins are installed from this source; uninstall them first")

var pluginName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// PluginSource is an index.toml that plugins install from.
type PluginSource struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// Installed is a plugin the owner installed, and the version a rollback
// returns to.
type Installed struct {
	Name            string  `json:"name"`
	Source          string  `json:"source"`
	Version         string  `json:"version"`
	SHA256          string  `json:"sha256"`
	PreviousSource  *string `json:"previous_source,omitempty"`
	PreviousVersion *string `json:"previous_version,omitempty"`
}

// Sources returns the sources, oldest first.
func (r *Registry) Sources(ctx context.Context) ([]PluginSource, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, url FROM plugin_source ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[PluginSource])
}

// AddSource adds an https:// or file:// index to install plugins from.
func (r *Registry) AddSource(ctx context.Context, s PluginSource) error {
	u, err := url.Parse(s.URL)
	if err != nil || !(u.Scheme == "https" && u.Host != "" || u.Scheme == "file" && filepath.IsAbs(u.Path)) {
		return fmt.Errorf("%w: source URL must be https:// or an absolute file:// path", ErrInvalid)
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO plugin_source (id, url) VALUES ($1, $2)`, s.ID, s.URL)
	return writeErr(err)
}

// DeleteSource removes a source that no installed plugin came from.
func (r *Registry) DeleteSource(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM plugin_source WHERE id = $1`, id)
	if db.IsCode(err, db.ForeignKeyViolation) {
		return ErrSourceInUse
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Installs returns the owner-installed plugins by name.
func (r *Registry) Installs(ctx context.Context) (map[string]Installed, error) {
	rows, err := r.pool.Query(ctx, `SELECT name, source, version, sha256, previous_source, previous_version
		FROM plugin_install WHERE version IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Installed])
	if err != nil {
		return nil, err
	}
	out := map[string]Installed{}
	for _, in := range list {
		out[in.Name] = in
	}
	return out, nil
}

// Install installs version of plugin name from source and makes it current.
// The current and previous versions stay on disk, and installing one of them
// again reuses its folder, so a rollback needs no download.
func (r *Registry) Install(ctx context.Context, source, name, version string) error {
	if !pluginName.MatchString(name) || version == "" || !filepath.IsLocal(version) || strings.ContainsAny(version, `/\`) {
		return fmt.Errorf("%w: plugin %q version %q", ErrInvalid, name, version)
	}
	r.installMu.Lock()
	defer r.installMu.Unlock()
	var srcURL string
	err := r.pool.QueryRow(ctx, `SELECT url FROM plugin_source WHERE id = $1`, source).Scan(&srcURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: no source %q", ErrInvalid, source)
	}
	if err != nil {
		return err
	}

	base := filepath.Join(r.dirs.Installed, name)
	folder := source + "@" + version
	sha, err := r.onDisk(ctx, name, source, version, filepath.Join(base, folder))
	if err != nil {
		return err
	}
	if sha == "" {
		if sha, err = r.download(ctx, srcURL, name, version, base, folder); err != nil {
			return err
		}
	}
	// ponytail: disk switches before the database records it; a failed
	// record leaves the new version loaded but unrecorded until the next install.
	if err := switchCurrent(base, folder); err != nil {
		return err
	}
	prev, err := r.record(ctx, source, name, version, sha)
	if err != nil {
		return err
	}
	keep := []string{"current", folder}
	if prev != "" {
		keep = append(keep, prev)
	}
	if err := prune(base, keep); err != nil {
		return err
	}
	return r.Sync(ctx)
}

// onDisk returns the sha256 of name's current or previous install when it is
// source's version and its folder still exists, or "" otherwise.
func (r *Registry) onDisk(ctx context.Context, name, source, version, dir string) (string, error) {
	var sha *string
	err := r.pool.QueryRow(ctx, `SELECT CASE
			WHEN source = $2 AND version = $3 THEN sha256
			WHEN previous_source = $2 AND previous_version = $3 THEN previous_sha256 END
		FROM plugin_install WHERE name = $1`, name, source, version).Scan(&sha)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && sha == nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin.toml")); err != nil {
		return "", nil
	}
	return *sha, nil
}

// download fetches name's version from the index at srcURL, checks it, and
// unpacks it into base/folder. It returns the tarball's sha256.
func (r *Registry) download(ctx context.Context, srcURL, name, version, base, folder string) (string, error) {
	index, err := url.Parse(srcURL)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSource, err)
	}
	idx, err := r.Index(ctx, srcURL)
	if err != nil {
		return "", err
	}
	entries := idx.Plugins
	i := slices.IndexFunc(entries, func(e Entry) bool { return e.Name == name && e.Version == version })
	if i < 0 {
		return "", fmt.Errorf("%w: %s %s is not in %s", ErrInvalid, name, version, index.Redacted())
	}
	e := entries[i]
	if e.Protocol != protocol.Version {
		return "", fmt.Errorf("%w: %s %s speaks protocol %d; this Innkeeper speaks %d",
			ErrInvalid, name, version, e.Protocol, protocol.Version)
	}
	j := slices.IndexFunc(e.Artifacts, func(a Artifact) bool { return a.OS == runtime.GOOS && a.Arch == runtime.GOARCH })
	if j < 0 {
		return "", fmt.Errorf("%w: %s %s has no build for %s/%s", ErrInvalid, name, version, runtime.GOOS, runtime.GOARCH)
	}
	a := e.Artifacts[j]
	u, err := artifactURL(index, a)
	if err != nil {
		return "", err
	}
	body, err := r.fetch(ctx, u, maxArtifact)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	if !strings.EqualFold(sha, a.SHA256) {
		return "", fmt.Errorf("%w: %s has sha256 %s; the index lists %s", ErrSource, u.Redacted(), sha, a.SHA256)
	}

	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(base, ".install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := untar(bytes.NewReader(body), tmp); err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrSource, u.Redacted(), err)
	}
	m, err := protocol.LoadManifest(tmp)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrSource, u.Redacted(), err)
	}
	if m.Name != name || m.Version != version || m.Protocol != protocol.Version {
		return "", fmt.Errorf("%w: %s holds %s %s protocol %d, not what the index lists", ErrSource, u.Redacted(), m.Name, m.Version, m.Protocol)
	}
	dst := filepath.Join(base, folder)
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return sha, nil
}

// record saves the install and returns the previous version's folder name,
// or "" if there is none.
func (r *Registry) record(ctx context.Context, source, name, version, sha string) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	// The old current version becomes the previous one, unless it is this same install.
	const same = `(plugin_install.source IS NOT DISTINCT FROM EXCLUDED.source AND plugin_install.version IS NOT DISTINCT FROM EXCLUDED.version)`
	var prevSource, prevVersion *string
	err = tx.QueryRow(ctx, `INSERT INTO plugin_install (name, source, version, sha256) VALUES ($1, $2, $3, $4)
		ON CONFLICT (name) DO UPDATE SET
			previous_source = CASE WHEN `+same+` THEN plugin_install.previous_source ELSE plugin_install.source END,
			previous_version = CASE WHEN `+same+` THEN plugin_install.previous_version ELSE plugin_install.version END,
			previous_sha256 = CASE WHEN `+same+` THEN plugin_install.previous_sha256 ELSE plugin_install.sha256 END,
			source = EXCLUDED.source, version = EXCLUDED.version, sha256 = EXCLUDED.sha256, changed_at = now()
		RETURNING previous_source, previous_version`, name, source, version, sha).Scan(&prevSource, &prevVersion)
	if err != nil {
		return "", err
	}
	// A new version may fix what broke the plugin's instances.
	if _, err := tx.Exec(ctx, `UPDATE plugin_instance SET broken_error = NULL, broken_at = NULL WHERE plugin = $1`, name); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `SELECT nextval('plugin_generation')`); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	if prevSource == nil || prevVersion == nil {
		return "", nil
	}
	return *prevSource + "@" + *prevVersion, nil
}

// switchCurrent points base/current at folder, replacing the link in one rename.
func switchCurrent(base, folder string) error {
	tmp := filepath.Join(base, ".current-new")
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Symlink(folder, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(base, "current"))
}

// prune removes everything in base but keep.
func prune(base string, keep []string) error {
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !slices.Contains(keep, e.Name()) {
			if err := os.RemoveAll(filepath.Join(base, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// untar unpacks a gzipped tarball of regular files and folders into dir,
// creating it. Links and paths that leave dir are refused.
func untar(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(h.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("tarball path %q leaves the folder", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
				return err
			}
			f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644|os.FileMode(h.Mode)&0o111)
			if err != nil {
				return err
			}
			n, err := io.Copy(f, io.LimitReader(tr, maxArtifact+1))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			if n > maxArtifact {
				return fmt.Errorf("tarball file %q is larger than %d bytes", h.Name, maxArtifact)
			}
		default:
			return fmt.Errorf("tarball entry %q is not a file or folder", h.Name)
		}
	}
}

// Uninstall removes an installed plugin's code. Its instances and data stay,
// so reinstalling brings them back. Uninstalling a plugin the image also
// ships falls back to the image's copy; that copy can't be uninstalled.
func (r *Registry) Uninstall(ctx context.Context, name string) error {
	if !pluginName.MatchString(name) {
		return ErrNotFound
	}
	r.installMu.Lock()
	defer r.installMu.Unlock()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE plugin_install SET source = NULL, version = NULL, sha256 = NULL,
			previous_source = NULL, previous_version = NULL, previous_sha256 = NULL, changed_at = now()
		WHERE name = $1 AND version IS NOT NULL`, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if r.IsBuiltin(name) {
			return fmt.Errorf("%w: %s is built in", ErrInvalid, name)
		}
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `SELECT nextval('plugin_generation')`); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(r.dirs.Installed, name)); err != nil {
		return err
	}
	return r.Sync(ctx)
}

// RemoveData deletes an uninstalled plugin's instances, database schema, and
// role. It can't be undone. It returns ErrInUse while cards are bound to one
// of the instances.
func (r *Registry) RemoveData(ctx context.Context, name string) error {
	if !pluginName.MatchString(name) {
		return ErrNotFound
	}
	r.installMu.Lock()
	defer r.installMu.Unlock()
	if _, ok := r.manifest(name); ok {
		return fmt.Errorf("%w: uninstall %s first", ErrInvalid, name)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := deleteInstances(ctx, tx, "plugin", name); err != nil {
		return err
	}
	var database string
	if err := tx.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		return err
	}
	schema, role := dbNames(database, name)
	var roleExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = $1)`, role).Scan(&roleExists); err != nil {
		return err
	}
	stmts := []string{`DROP SCHEMA IF EXISTS ` + pgx.Identifier{schema}.Sanitize() + ` CASCADE`}
	if roleExists {
		rid := pgx.Identifier{role}.Sanitize()
		stmts = append(stmts, `DROP OWNED BY `+rid, `DROP ROLE `+rid)
	}
	for _, q := range stmts {
		if _, err := tx.Exec(ctx, q); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM plugin_install WHERE name = $1`, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
