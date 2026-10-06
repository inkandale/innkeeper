package library

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenThumb(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	avatar, err := os.Open(filepath.Join("..", "cardhash", "testdata", "avatar.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer avatar.Close()
	b, err := lib.PutBlob(ctx, avatar)
	if err != nil {
		t.Fatal(err)
	}

	// The fixture is 256×320.
	for w, want := range map[int][2]int{160: {160, 200}, 320: {256, 320}} {
		f, err := lib.OpenThumb(ctx, b.SHA256, w)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := jpeg.DecodeConfig(f)
		f.Close()
		if err != nil || cfg.Width != want[0] || cfg.Height != want[1] {
			t.Fatalf("w=%d: %d×%d, %v; want %v", w, cfg.Width, cfg.Height, err, want)
		}
	}
	// The second request reads the cached file.
	dst, err := lib.blobs.ThumbPath(b.SHA256, 160)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := lib.OpenThumb(ctx, b.SHA256, 160)
	if err != nil {
		t.Fatal(err)
	}
	cached, _ := io.ReadAll(f)
	f.Close()
	if string(cached) != "cached" {
		t.Fatalf("second request = %q, want the cached file", cached)
	}

	// A tall image fits in w × 4w, since JPEG can't encode a side of 65536 or more.
	var tall bytes.Buffer
	if err := png.Encode(&tall, image.NewGray(image.Rect(0, 0, 10, 100000))); err != nil {
		t.Fatal(err)
	}
	tb, err := lib.PutBlob(ctx, &tall)
	if err != nil {
		t.Fatal(err)
	}
	f, err = lib.OpenThumb(ctx, tb.SHA256, 160)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(f)
	f.Close()
	if err != nil || cfg.Width != 1 || cfg.Height != 640 {
		t.Fatalf("tall thumb: %d×%d, %v", cfg.Width, cfg.Height, err)
	}

	if _, err := lib.OpenThumb(ctx, b.SHA256, 300); !errors.Is(err, ErrInvalid) {
		t.Fatalf("odd width: %v", err)
	}
	text, err := lib.PutBlob(ctx, strings.NewReader("not an image"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.OpenThumb(ctx, text.SHA256, 160); !errors.Is(err, ErrInvalid) {
		t.Fatalf("text blob: %v", err)
	}
	if _, err := lib.OpenThumb(ctx, strings.Repeat("0", 64), 160); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing blob: %v", err)
	}
}
