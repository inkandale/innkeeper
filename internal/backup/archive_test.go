package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeBackup hand-builds a finished backup folder; extra maps relative paths
// to contents.
func fakeBackup(t *testing.T, dir, name string, m Manifest, extra map[string]string) {
	t.Helper()
	folder := filepath.Join(dir, string(m.Type), name)
	b, _ := json.Marshal(m)
	extra["manifest.json"] = string(b)
	for p, c := range extra {
		full := filepath.Join(folder, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// archiveOf builds a gzip tar from name/content pairs.
func archiveOf(t *testing.T, files ...[2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		tw.WriteHeader(&tar.Header{Name: f[0], Size: int64(len(f[1])), Mode: 0o600, Typeflag: tar.TypeReg})
		tw.Write([]byte(f[1]))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func entries(t *testing.T, archive []byte) (names []string, bodies map[string]string) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	bodies = map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Mode != 0o600 || h.Typeflag != tar.TypeReg {
			t.Errorf("%s: mode %o type %c", h.Name, h.Mode, h.Typeflag)
		}
		b, _ := io.ReadAll(tr)
		names = append(names, h.Name)
		bodies[h.Name] = string(b)
	}
}

const sha = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

func TestWriteArchiveAssets(t *testing.T) {
	dir := t.TempDir()
	name := "assets-20260101T000000.000000Z"
	m := Manifest{Format: 1, Type: Assets, Blobs: []string{sha}, Tables: map[string]int64{"blob": 1, "card": 1}}
	fakeBackup(t, dir, name, m, map[string]string{"tables/blob.copy": "b", "tables/card.copy": "c"})
	bp := blobPath(dir, sha)
	os.MkdirAll(filepath.Dir(bp), 0o700)
	os.WriteFile(bp, []byte("blobdata"), 0o600)

	var buf bytes.Buffer
	if err := WriteArchive(&buf, dir, name); err != nil {
		t.Fatal(err)
	}
	names, bodies := entries(t, buf.Bytes())
	want := []string{"manifest.json", "blobs/" + sha, "tables/blob.copy", "tables/card.copy"}
	if !slices.Equal(names, want) {
		t.Fatalf("entries %v, want %v", names, want)
	}
	if bodies["blobs/"+sha] != "blobdata" {
		t.Errorf("blob body %q", bodies["blobs/"+sha])
	}
	got, err := ReadManifest(bytes.NewReader(buf.Bytes()))
	if err != nil || got.Type != Assets || !slices.Equal(got.Blobs, m.Blobs) {
		t.Errorf("ReadManifest = %+v, %v", got, err)
	}
}

func TestWriteArchiveConfigPlugins(t *testing.T) {
	dir := t.TempDir()
	name := "config-20260101T000000.000000Z"
	fakeBackup(t, dir, name, Manifest{Format: 1, Type: Config, Tables: map[string]int64{"setting": 1, "plugin_instance": 1}}, map[string]string{
		"tables/setting.copy": "s", "tables/plugin_instance.copy": "p", "plugins.sql": "SELECT 1;",
	})
	var buf bytes.Buffer
	if err := WriteArchive(&buf, dir, name); err != nil {
		t.Fatal(err)
	}
	names, _ := entries(t, buf.Bytes())
	// Load order: plugin_instance before setting; tables absent from the folder are skipped.
	want := []string{"manifest.json", "tables/plugin_instance.copy", "tables/setting.copy", "plugins.sql"}
	if !slices.Equal(names, want) {
		t.Fatalf("entries %v, want %v", names, want)
	}
}

func TestReadManifestRejects(t *testing.T) {
	cases := map[string][]byte{
		"not gzip":     []byte("hello"),
		"wrong first":  archiveOf(t, [2]string{"tables/x.copy", "x"}),
		"format 2":     archiveOf(t, [2]string{"manifest.json", `{"format":2,"type":"assets"}`}),
		"unknown type": archiveOf(t, [2]string{"manifest.json", `{"format":1,"type":"bogus"}`}),
		"bad json":     archiveOf(t, [2]string{"manifest.json", `{`}),
	}
	for name, b := range cases {
		if _, err := ReadManifest(bytes.NewReader(b)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, err := ReadManifest(bytes.NewReader(archiveOf(t, [2]string{"manifest.json", `{"format":1,"type":"config"}`}))); err != nil {
		t.Errorf("valid manifest: %v", err)
	}
}

func TestWriteArchiveRejects(t *testing.T) {
	dir := t.TempDir()
	fakeBackup(t, dir, "assets-x.part", Manifest{Format: 1, Type: Assets}, map[string]string{})
	for _, name := range []string{"../x", "assets-x.part", "bogus-1", "assets-missing", "assets-a/../../x", ""} {
		err := WriteArchive(io.Discard, dir, name)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%q: err = %v, want ErrNotExist", name, err)
		}
	}
	// A manifest whose type disagrees with the name isn't served.
	fakeBackup(t, dir, "assets-1", Manifest{Format: 1, Type: Config}, map[string]string{})
	os.Rename(filepath.Join(dir, "config", "assets-1"), filepath.Join(dir, "assets", "assets-1"))
	if err := WriteArchive(io.Discard, dir, "assets-1"); err == nil {
		t.Error("type mismatch: want error")
	}
}

func TestWriteArchiveRejectsDamaged(t *testing.T) {
	dir := t.TempDir()
	// Table listed but its file is absent.
	fakeBackup(t, dir, "config-1", Manifest{Format: 1, Type: Config, Tables: map[string]int64{"setting": 1}}, map[string]string{})
	// Blob listed but absent from the store.
	fakeBackup(t, dir, "assets-1", Manifest{Format: 1, Type: Assets, Blobs: []string{sha}}, map[string]string{})
	for _, name := range []string{"config-1", "assets-1"} {
		var buf bytes.Buffer
		if err := WriteArchive(&buf, dir, name); err == nil || errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: err = %v, want a damaged-backup error", name, err)
		}
		if buf.Len() != 0 {
			t.Errorf("%s: wrote %d bytes", name, buf.Len())
		}
	}
}
