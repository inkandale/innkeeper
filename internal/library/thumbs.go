package library

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"slices"
	"strings"

	"golang.org/x/image/draw"

	"github.com/inkandale/innkeeper/cardhash"
)

// ThumbWidths are the thumbnail widths OpenThumb makes. Few, so the cache
// stays small.
var ThumbWidths = []int{160, 320, 640}

// OpenThumb returns a JPEG of an image blob, w pixels wide or its own width
// if narrower, making and caching it on first use. The caller closes the file.
func (l *Library) OpenThumb(ctx context.Context, sha string, w int) (*os.File, error) {
	if !slices.Contains(ThumbWidths, w) {
		return nil, fmt.Errorf("%w: thumbnail width must be one of %v", ErrInvalid, ThumbWidths)
	}
	b, err := l.Blob(ctx, sha)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(b.MIME, "image/") {
		return nil, fmt.Errorf("%w: blob is %s, not an image", ErrInvalid, b.MIME)
	}
	dst, err := l.blobs.ThumbPath(sha, w)
	if err != nil {
		return nil, err
	}
	if f, err := os.Open(dst); err == nil {
		return f, nil
	}
	src, err := l.blobs.Path(sha)
	if err != nil {
		return nil, err
	}
	img, err := cardhash.DecodeImage(src)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	r := img.Bounds()
	// Fit in w × 4w, never upscaling: JPEG can't encode a side of 65536 or more.
	scale := min(1, float64(w)/float64(r.Dx()), float64(4*w)/float64(r.Dy()))
	tw, th := max(1, int(float64(r.Dx())*scale)), max(1, int(float64(r.Dy())*scale))
	// JPEG has no alpha, so transparent pixels land on white.
	out := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.Draw(out, out.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(out, out.Bounds(), img, r, draw.Over, nil)
	if err := l.blobs.WriteThumb(dst, func(f io.Writer) error {
		return jpeg.Encode(f, out, &jpeg.Options{Quality: 82})
	}); err != nil {
		return nil, err
	}
	return os.Open(dst)
}
