package api

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
)

func TestTagsAndCollections(t *testing.T) {
	e := newEnv(t)
	c := e.createCard(`{"name": "Brakka", "tags": ["fantasy"]}`)
	var tags []library.TagCount
	e.call("GET", "/tags", nil, 200, &tags)
	if len(tags) != 1 || tags[0] != (library.TagCount{Name: "fantasy", Cards: 1}) {
		t.Fatalf("tags = %+v", tags)
	}

	var col library.Collection
	e.call("POST", "/collections", map[string]string{"name": "Favorites"}, 201, &col)
	e.call("POST", "/collections", map[string]string{"name": "Favorites"}, 409, nil)
	path := fmt.Sprintf("/collections/%d", col.ID)
	e.call("PUT", fmt.Sprintf("%s/cards/%d", path, c.ID), nil, 204, nil)
	e.call("PUT", fmt.Sprintf("/collections/999/cards/%d", c.ID), nil, 404, nil)
	e.call("PATCH", path, map[string]string{"name": "Best"}, 200, &col)
	if col.Name != "Best" {
		t.Fatalf("renamed = %+v", col)
	}
	var list []library.Collection
	e.call("GET", "/collections", nil, 200, &list)
	if len(list) != 1 || list[0].Cards != 1 || list[0].Name != "Best" {
		t.Fatalf("collections = %+v", list)
	}
	e.call("PUT", fmt.Sprintf("%s/cards/999", path), nil, 404, nil)
	e.call("DELETE", fmt.Sprintf("%s/cards/%d", path, c.ID), nil, 204, nil)
	e.call("DELETE", path, nil, 204, nil)
	e.call("DELETE", path, nil, 404, nil)
}

func TestBlobs(t *testing.T) {
	e := newEnv(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	var b library.Blob
	e.call("POST", "/blobs", png, 201, &b)
	if b.MIME != "image/png" || b.Size != int64(len(png)) {
		t.Fatalf("blob = %+v", b)
	}
	r := e.call("GET", "/blobs/"+b.SHA256, nil, 200, nil)
	if !bytes.Equal(r.Body, png) || r.Header.Get("Content-Type") != "image/png" ||
		r.Header.Get("X-Content-Type-Options") != "nosniff" || r.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("download: %d bytes, %v", len(r.Body), r.Header)
	}

	// Anything but an image downloads instead of rendering.
	b = library.Blob{}
	e.call("POST", "/blobs", []byte("<html><script>alert(1)</script></html>"), 201, &b)
	r = e.call("GET", "/blobs/"+b.SHA256, nil, 200, nil)
	if r.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("html blob served as %q", r.Header.Get("Content-Type"))
	}
	if r.Header.Get("X-Content-Type-Options") != "nosniff" || r.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("html blob headers = %v", r.Header)
	}
	e.call("POST", "/blobs", []byte{}, 400, nil)

	e.call("GET", "/blobs/"+strings.Repeat("0", 64), nil, 404, nil)
	e.call("GET", "/blobs/not-a-hash", nil, 422, nil)

	avatar, err := os.ReadFile(filepath.Join("..", "..", "cardhash", "testdata", "avatar.png"))
	if err != nil {
		t.Fatal(err)
	}
	b = library.Blob{}
	e.call("POST", "/blobs", avatar, 201, &b)
	r = e.call("GET", "/blobs/"+b.SHA256+"?w=160", nil, 200, nil)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(r.Body))
	if err != nil || cfg.Width != 160 || r.Header.Get("Content-Type") != "image/jpeg" ||
		r.Header.Get("X-Content-Type-Options") != "nosniff" || r.Header.Get("Content-Security-Policy") != "sandbox" ||
		r.Header.Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("thumb: %v, %v, %v", cfg, err, r.Header)
	}
	e.call("GET", "/blobs/"+b.SHA256+"?w=300", nil, 400, nil)
}
