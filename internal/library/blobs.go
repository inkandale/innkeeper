package library

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/davidgibbons/innkeeper/cardhash"
)

// Blob is a stored file.
type Blob struct {
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	MIME        string `json:"mime"`
	Store       string `json:"store"`
	AvatarPhash *int64 `json:"avatar_phash,omitempty"`
	// PixelHash is the SHA-256 of an image's decoded pixels; nil for other blobs.
	PixelHash *string   `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

const blobCols = `sha256, size, mime, store, avatar_phash, pixel_hash, created_at`

// MaxBlobBytes caps a stored file. Card PNGs are a few MB at most.
const MaxBlobBytes = 32 << 20

// PutBlob stores r and records it.
func (l *Library) PutBlob(ctx context.Context, r io.Reader) (Blob, error) {
	sha, size, err := l.blobs.Put(r)
	if err != nil {
		return Blob{}, err
	}
	return l.record(ctx, sha, size)
}

// IngestBlob moves a file a plugin wrote to the store's tmp folder into the
// store and records it. path is untrusted; see blobstore.Store.Ingest.
func (l *Library) IngestBlob(ctx context.Context, path string) (Blob, error) {
	sha, size, err := l.blobs.Ingest(path, MaxBlobBytes)
	if err != nil {
		return Blob{}, err
	}
	return l.record(ctx, sha, size)
}

// Blob returns a blob's record.
func (l *Library) Blob(ctx context.Context, sha string) (Blob, error) {
	return scanBlob(l.pool.QueryRow(ctx, `SELECT `+blobCols+` FROM blob WHERE sha256 = $1`, sha))
}

func scanBlob(row interface{ Scan(...any) error }) (Blob, error) {
	var b Blob
	err := row.Scan(&b.SHA256, &b.Size, &b.MIME, &b.Store, &b.AvatarPhash, &b.PixelHash, &b.CreatedAt)
	return b, mapErr(err)
}

// record inserts the blob row. Images get an avatar_phash and pixel_hash; an image that
// fails to decode is still stored, just without one.
//
// A blob stored again gets a fresh created_at, so garbage collection gives
// the caller a full grace period to reference it.
func (l *Library) record(ctx context.Context, sha string, size int64) (Blob, error) {
	if b, err := scanBlob(l.pool.QueryRow(ctx, `UPDATE blob SET created_at = now() WHERE sha256 = $1
		RETURNING `+blobCols, sha)); !errors.Is(err, ErrNotFound) {
		return b, err
	}
	path, err := l.blobs.Path(sha)
	if err != nil {
		return Blob{}, err
	}
	mime, err := sniff(path)
	if err != nil {
		return Blob{}, err
	}
	var phash *int64
	var pixels *string
	if strings.HasPrefix(mime, "image/") {
		if img, err := cardhash.DecodeImage(path); err == nil {
			h, p := cardhash.Phash(img), cardhash.PixelHash(img)
			phash, pixels = &h, &p
		}
	}
	if _, err := l.pool.Exec(ctx, `INSERT INTO blob (sha256, size, mime, store, avatar_phash, pixel_hash)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (sha256) DO UPDATE SET created_at = now()`,
		sha, size, mime, l.blobs.Name, phash, pixels); err != nil {
		return Blob{}, err
	}
	return l.Blob(ctx, sha)
}

func sniff(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}
	return http.DetectContentType(head[:n]), nil
}

// OpenBlob returns a blob's record and its file. The caller closes the file.
func (l *Library) OpenBlob(ctx context.Context, sha string) (Blob, *os.File, error) {
	b, err := l.Blob(ctx, sha)
	if err != nil {
		return Blob{}, nil, err
	}
	path, err := l.blobs.Path(sha)
	if err != nil {
		return Blob{}, nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Blob{}, nil, ErrNotFound
	}
	return b, f, err
}

// BlobPath returns where a blob's file lives.
func (l *Library) BlobPath(sha string) (string, error) { return l.blobs.Path(sha) }

// SameImage reports whether blobs a and b are the same picture: the same
// bytes, or images with the same pixels. Either may be "", meaning no
// avatar. A blob stored before pixel_hash existed gets one now.
func (l *Library) SameImage(ctx context.Context, a, b string) (bool, error) {
	if a == b {
		return true, nil
	}
	if a == "" || b == "" {
		return false, nil
	}
	pa, err := l.PixelHash(ctx, a)
	if err != nil || pa == "" {
		return false, err
	}
	pb, err := l.PixelHash(ctx, b)
	return pb != "" && pa == pb, err
}

// PixelHash returns the blob's pixel_hash, computing and storing it if
// missing; "" when the blob isn't an image that decodes.
func (l *Library) PixelHash(ctx context.Context, sha string) (string, error) {
	blob, err := l.Blob(ctx, sha)
	if err != nil {
		return "", err
	}
	if blob.PixelHash != nil {
		return *blob.PixelHash, nil
	}
	if !strings.HasPrefix(blob.MIME, "image/") {
		return "", nil
	}
	path, err := l.blobs.Path(sha)
	if err != nil {
		return "", err
	}
	img, err := cardhash.DecodeImage(path)
	if err != nil {
		return "", nil
	}
	p := cardhash.PixelHash(img)
	_, err = l.pool.Exec(ctx, `UPDATE blob SET pixel_hash = $2 WHERE sha256 = $1`, sha, p)
	return p, err
}
