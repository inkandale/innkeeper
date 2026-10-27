package testutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

// PluginRelease is one plugin version for PluginSource.
type PluginRelease struct {
	Name, Version string
	// Protocol is the manifest's and index's protocol; 0 means protocol.Version.
	Protocol int
	// SHA256 overrides the index's hash; "" lists the tarball's real one.
	SHA256 string
	// Uses are the service methods the plugin calls, in its manifest and index entry.
	Uses []string
}

// PluginSource writes a tarball per release and an index.toml listing them
// for this platform into a new folder, and returns the index's file:// URL.
func PluginSource(t *testing.T, releases ...PluginRelease) string {
	t.Helper()
	dir := t.TempDir()
	var index strings.Builder
	for _, r := range releases {
		if r.Protocol == 0 {
			r.Protocol = protocol.Version
		}
		tgz := PluginTarball(t, r)
		file := fmt.Sprintf("%s-%s.tar.gz", r.Name, r.Version)
		if err := os.WriteFile(filepath.Join(dir, file), tgz, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(tgz)
		hash := hex.EncodeToString(sum[:])
		if r.SHA256 != "" {
			hash = r.SHA256
		}
		fmt.Fprintf(&index, "[[plugin]]\nname = %q\nversion = %q\nprotocol = %d\ncapabilities = [\"import\"]\n%sdescription = \"Test plugin.\"\n\n"+
			"[[plugin.artifact]]\nos = %q\narch = %q\nurl = %q\nsha256 = %q\n\n",
			r.Name, r.Version, r.Protocol, usesLine(r.Uses), runtime.GOOS, runtime.GOARCH, file, hash)
	}
	path := filepath.Join(dir, "index.toml")
	if err := os.WriteFile(path, []byte(index.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return "file://" + path
}

// PluginTarball returns a gzipped tarball holding a plugin.toml and a stub
// binary, as innkeeper-plugins publishes them.
func PluginTarball(t *testing.T, r PluginRelease) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	name := r.Name
	manifest := fmt.Sprintf("name = %q\nversion = %q\nprotocol = %d\ncommand = [\"./%s\"]\ncapabilities = [\"import\"]\n%s",
		name, r.Version, r.Protocol, name, usesLine(r.Uses))
	for _, f := range []struct {
		name string
		mode int64
		body string
	}{{"plugin.toml", 0o644, manifest}, {name, 0o755, "#!/bin/sh\n"}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// usesLine is a TOML uses line, or nothing without uses.
func usesLine(uses []string) string {
	if len(uses) == 0 {
		return ""
	}
	quoted := make([]string, len(uses))
	for i, u := range uses {
		quoted[i] = strconv.Quote(u)
	}
	return "uses = [" + strings.Join(quoted, ", ") + "]\n"
}
