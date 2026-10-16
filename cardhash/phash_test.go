package cardhash

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scene draws a gradient with a disk at any size, so the same picture can be
// rendered at two resolutions. invert swaps light and dark.
func scene(t *testing.T, w, h int, invert bool) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			v := uint8(255 * fx * (1 - fy))
			if dx, dy := fx-0.6, fy-0.4; dx*dx+dy*dy < 0.04 {
				v = 255 - v/2
			}
			if invert {
				v = 255 - v
			}
			img.Set(x, y, color.RGBA{v, v / 2, 255 - v, 255})
		}
	}
	path := filepath.Join(t.TempDir(), "scene.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func phash(t *testing.T, path string) int64 {
	t.Helper()
	h, err := ImagePhash(path)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// imagehash.phash(Image.open("testdata/avatar.png")) in Python, as hex.
var avatarPhashPython = int64(-0x6767989867639365) // 98986767989c6c9b

func TestImagePhashVector(t *testing.T) {
	got := phash(t, filepath.Join("testdata", "avatar.png"))
	if d := Distance(got, avatarPhashPython); d > 6 {
		t.Fatalf("got %016x, %d bits from imagehash's %016x", uint64(got), d, uint64(avatarPhashPython))
	}
}

func TestImagePhashSimilarity(t *testing.T) {
	big := phash(t, scene(t, 400, 500, false))
	small := phash(t, scene(t, 100, 125, false))
	inverted := phash(t, scene(t, 400, 500, true))
	t.Logf("resized %d bits, inverted %d bits", Distance(big, small), Distance(big, inverted))
	if d := Distance(big, small); d > 6 {
		t.Errorf("resized copy is %d bits away, want <= 6", d)
	}
	if d := Distance(big, inverted); d < 20 {
		t.Errorf("inverted image is %d bits away, want >= 20", d)
	}
}

func TestImagePhashRejectsNonImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.txt")
	os.WriteFile(path, []byte("not an image"), 0o644)
	if _, err := ImagePhash(path); err == nil {
		t.Fatal("hashed a text file")
	}
}

func TestPhashMatchesImagePhash(t *testing.T) {
	path := filepath.Join("testdata", "avatar.png")
	want, err := ImagePhash(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := DecodeImageBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := Phash(img); got != want {
		t.Fatalf("Phash = %x, ImagePhash = %x", got, want)
	}
}

func TestDecodeImageBytesRefusesHuge(t *testing.T) {
	// A PNG header claiming 100000×100000 pixels.
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := []byte{0, 1, 0x86, 0xa0, 0, 1, 0x86, 0xa0, 8, 2, 0, 0, 0}
	binary.Write(&buf, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	buf.Write(chunk)
	binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	if _, err := DecodeImageBytes(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "pixel limit") {
		t.Fatalf("err = %v, want the pixel limit", err)
	}
}

func TestPixelHashIgnoresEncoding(t *testing.T) {
	gray := image.NewGray(image.Rect(0, 0, 4, 4))
	rgba := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			v := uint8(x*40 + y*10)
			gray.SetGray(x, y, color.Gray{Y: v})
			rgba.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: 255})
		}
	}
	if PixelHash(gray) != PixelHash(rgba) {
		t.Error("same pixels in two color models hash differently")
	}
	rgba.SetNRGBA(0, 0, color.NRGBA{R: 1, G: 0, B: 0, A: 255})
	if PixelHash(gray) == PixelHash(rgba) {
		t.Error("one changed pixel hashes the same")
	}
}

func TestPixelHashIncludesSize(t *testing.T) {
	a := image.NewGray(image.Rect(0, 0, 2, 8))
	b := image.NewGray(image.Rect(0, 0, 8, 2))
	if PixelHash(a) == PixelHash(b) {
		t.Error("2×8 and 8×2 of the same pixels hash the same")
	}
}
