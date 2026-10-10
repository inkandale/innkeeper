package cardhash

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"slices"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Images are untrusted; refuse ones whose decoded size could exhaust memory.
const maxPixels = 64 << 20

// DecodeImage decodes the image at path, refusing one whose decoded size
// could exhaust memory.
func DecodeImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return decodeImage(f)
}

// DecodeImageBytes decodes b with DecodeImage's pixel limit.
func DecodeImageBytes(b []byte) (image.Image, error) {
	return decodeImage(bytes.NewReader(b))
}

func decodeImage(r io.ReadSeeker) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return nil, err
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("image is %d×%d, over the %d pixel limit", cfg.Width, cfg.Height, maxPixels)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(r)
	return img, err
}

// ImagePhash returns Phash of the image at path.
func ImagePhash(path string) (int64, error) {
	img, err := DecodeImage(path)
	if err != nil {
		return 0, err
	}
	return Phash(img), nil
}

// Phash returns the 64-bit DCT perceptual hash of img. It follows
// imagehash.phash from Python: grayscale, resize to 32×32, 2D DCT-II, keep
// the top-left 8×8 block, and set a bit (row-major, first is the high bit)
// for each value above the block's median.
//
// ponytail: resampling differs from PIL's, so on some images a Python port
// lands a few bits away; the Hamming threshold absorbs it.
func Phash(img image.Image) int64 {
	const n = 32
	gray := image.NewGray(image.Rect(0, 0, n, n))
	draw.CatmullRom.Scale(gray, gray.Bounds(), img, img.Bounds(), draw.Src, nil)

	var cos [8][n]float64
	for k := range 8 {
		for i := range n {
			cos[k][i] = math.Cos(math.Pi * float64(k) * float64(2*i+1) / (2 * n))
		}
	}
	var low [64]float64
	for u := range 8 {
		for v := range 8 {
			var s float64
			for y := range n {
				for x := range n {
					s += float64(gray.GrayAt(x, y).Y) * cos[u][y] * cos[v][x]
				}
			}
			low[u*8+v] = s
		}
	}
	sorted := slices.Clone(low[:])
	slices.Sort(sorted)
	median := (sorted[31] + sorted[32]) / 2
	var out uint64
	for _, s := range low {
		out <<= 1
		if s > median {
			out |= 1
		}
	}
	return int64(out)
}
