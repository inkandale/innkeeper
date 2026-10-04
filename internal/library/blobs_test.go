package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPutBlob(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	png, err := os.Open(filepath.Join("..", "cardhash", "testdata", "avatar.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer png.Close()
	img, err := lib.PutBlob(ctx, png)
	if err != nil {
		t.Fatal(err)
	}
	if img.MIME != "image/png" || img.AvatarPhash == nil || img.Store != "library" {
		t.Fatalf("image blob = %+v", img)
	}

	txt, err := lib.PutBlob(ctx, strings.NewReader("plain text"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(txt.MIME, "text/plain") || txt.AvatarPhash != nil {
		t.Fatalf("text blob = %+v", txt)
	}
	again, err := lib.PutBlob(ctx, strings.NewReader("plain text"))
	if err != nil || again.SHA256 != txt.SHA256 {
		t.Fatalf("second put = %+v, %v", again, err)
	}

	// A card can use the image as its avatar.
	if _, _, err := lib.Cards.Create(ctx, "a", "upload", Edit{Data: []byte(`{"name": "A"}`), Source: "ui", AvatarBlob: img.SHA256}); err != nil {
		t.Fatal(err)
	}
}

func TestIngestBlob(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	os.MkdirAll(lib.blobs.TmpDir(), 0o755)
	src := filepath.Join(lib.blobs.TmpDir(), "from-plugin")
	os.WriteFile(src, []byte("plugin bytes"), 0o644)
	b, err := lib.IngestBlob(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if b.Size != 12 {
		t.Fatalf("blob = %+v", b)
	}
	if _, err := lib.IngestBlob(ctx, "/etc/hosts"); err == nil {
		t.Fatal("ingested a file outside tmp")
	}
}
