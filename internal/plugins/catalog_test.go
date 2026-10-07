package plugins

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const testIndex = `[[plugin]]
name = "good"
version = "1.0.0"
protocol = 1
capabilities = ["import"]
description = "A good plugin."
unknown_key = "ignored"

[[plugin.artifact]]
os = "linux"
arch = "amd64"
url = "good-1.0.0-linux-amd64.tar.gz"
sha256 = "abc"
`

func TestIndex(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testIndex))
	}))
	defer srv.Close()
	reg := &Registry{client: srv.Client()}

	got, err := reg.Index(ctx, srv.URL+"/index.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "good" || len(got[0].Artifacts) != 1 || got[0].Artifacts[0].Arch != "amd64" {
		t.Fatalf("got %+v", got)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.toml"), []byte(testIndex), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := reg.Index(ctx, "file://"+filepath.Join(dir, "index.toml")); err != nil || len(got) != 1 {
		t.Fatalf("file source: %+v, %v", got, err)
	}

	if _, err := reg.Index(ctx, "http://example.com/index.toml"); !errors.Is(err, ErrSource) {
		t.Fatalf("http source: got %v, want ErrSource", err)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.0.1", "1.0.0", true},
		{"1.10.0", "1.9.0", true},
		{"1.0.0", "1.0.0", false},
		{"1.0", "1.0.1", false},
		{"v2.0.0", "1.9.9", true},
		{"1.0.0-rc1", "1.0.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
