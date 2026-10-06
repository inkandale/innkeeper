package cardhash

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
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
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("image is %d×%d, over the %d pixel limit", cfg.Width, cfg.Height, maxPixels)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	return img, err
}

// ImagePhash returns the 64-bit DCT perceptual hash of the image at path. It
// follows imagehash.phash from Python: grayscale, resize to 32×32, 2D DCT-II,
// keep the top-left 8×8 block, and set a bit (row-major, first is the high
// bit) for each value above the block's median.
//
// ponytail: resampling differs from PIL's, so on some images a Python port
// lands a few bits away; the Hamming threshold absorbs it.
func ImagePhash(path string) (int64, error) {
	img, err := DecodeImage(path)
	if err != nil {
		return 0, err
	}
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
	return int64(out), nil
}
