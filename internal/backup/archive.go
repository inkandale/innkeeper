package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// maxManifest bounds the manifest read from an untrusted upload.
const maxManifest = 64 << 20

// WriteArchive streams backup name in dir as a gzip tar: manifest.json,
// blobs/, tables/, then plugins/.
func WriteArchive(w io.Writer, dir, name string) error {
	t := Type(strings.SplitN(name, "-", 2)[0])
	// name becomes a path, so it must be a plain finished-backup folder name.
	if _, ok := tables[t]; !ok || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") || strings.HasSuffix(name, ".part") {
		return fmt.Errorf("backup %q: %w", name, fs.ErrNotExist)
	}
	folder := filepath.Join(dir, string(t), name)
	m, err := readManifest(folder)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("backup %q: %w", name, fs.ErrNotExist)
		}
		return fmt.Errorf("backup %q: %w", name, err)
	}
	if m.Type != t {
		return fmt.Errorf("backup %q: manifest type is %q", name, m.Type)
	}

	// Checked before the first byte so a damaged backup fails cleanly instead
	// of streaming a truncated archive.
	for table := range m.Tables {
		if !slices.Contains(tables[t], table) {
			return fmt.Errorf("backup %q: manifest lists unknown table %q", name, table)
		}
		if err := mustExist(filepath.Join(folder, "tables", table+".copy")); err != nil {
			return fmt.Errorf("backup %q: %w", name, err)
		}
	}
	for _, sha := range m.Blobs {
		if !validSHA(sha) {
			return fmt.Errorf("backup %q: manifest lists bad blob hash %q", name, sha)
		}
		if err := mustExist(blobPath(dir, sha)); err != nil {
			return fmt.Errorf("backup %q: %w", name, err)
		}
	}

	for _, schema := range m.PluginSchemas {
		if !validSchema(schema) {
			return fmt.Errorf("backup %q: manifest lists bad plugin schema %q", name, schema)
		}
		if err := mustExist(filepath.Join(folder, "plugins", schema+".sql")); err != nil {
			return fmt.Errorf("backup %q: %w", name, err)
		}
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if err := addFile(tw, "manifest.json", filepath.Join(folder, "manifest.json")); err != nil {
		return err
	}
	for _, sha := range m.Blobs {
		if err := addFile(tw, "blobs/"+sha, blobPath(dir, sha)); err != nil {
			return err
		}
	}
	for _, table := range tables[t] {
		if _, ok := m.Tables[table]; !ok {
			continue
		}
		if err := addFile(tw, "tables/"+table+".copy", filepath.Join(folder, "tables", table+".copy")); err != nil {
			return err
		}
	}
	for _, schema := range m.PluginSchemas {
		if err := addFile(tw, "plugins/"+schema+".sql", filepath.Join(folder, "plugins", schema+".sql")); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func mustExist(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("missing %s", path)
	}
	return nil
}

func addFile(tw *tar.Writer, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: fi.Size(), ModTime: fi.ModTime(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// ReadManifest reads an archive's first entry, which must be manifest.json.
func ReadManifest(r io.Reader) (Manifest, error) {
	_, m, err := openArchive(r)
	return m, err
}

// openArchive validates the manifest entry and returns the tar positioned
// after it, so a restore can keep reading entries in the same pass.
func openArchive(r io.Reader) (*tar.Reader, Manifest, error) {
	var m Manifest
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, m, fmt.Errorf("not a gzip archive: %w", err)
	}
	tr := tar.NewReader(gz)
	h, err := tr.Next()
	if err != nil {
		return nil, m, fmt.Errorf("reading archive: %w", err)
	}
	if h.Name != "manifest.json" {
		return nil, m, fmt.Errorf("archive must start with manifest.json, found %q", h.Name)
	}
	b, err := io.ReadAll(io.LimitReader(tr, maxManifest+1))
	if err != nil {
		return nil, m, fmt.Errorf("reading manifest.json: %w", err)
	}
	if len(b) > maxManifest {
		return nil, m, errors.New("manifest.json is too large")
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, m, fmt.Errorf("invalid manifest.json: %w", err)
	}
	if m.Format != 1 {
		return nil, m, fmt.Errorf("unsupported backup format %d", m.Format)
	}
	if !slices.Contains(Types, m.Type) {
		return nil, m, fmt.Errorf("unknown backup type %q", m.Type)
	}
	return tr, m, nil
}
