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

	"github.com/davidgibbons/innkeeper/internal/cardhash"
)

// Blob is a stored file.
type Blob struct {
	SHA256      string    `json:"sha256"`
	Size        int64     `json:"size"`
	MIME        string    `json:"mime"`
	Store       string    `json:"store"`
	AvatarPhash *int64    `json:"avatar_phash,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

const blobCols = `sha256, size, mime, store, avatar_phash, created_at`

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
	err := row.Scan(&b.SHA256, &b.Size, &b.MIME, &b.Store, &b.AvatarPhash, &b.CreatedAt)
	return b, mapErr(err)
}

// record inserts the blob row. Images get an avatar_phash; an image that
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
	if strings.HasPrefix(mime, "image/") {
		if h, err := cardhash.ImagePhash(path); err == nil {
			phash = &h
		}
	}
	if _, err := l.pool.Exec(ctx, `INSERT INTO blob (sha256, size, mime, store, avatar_phash)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (sha256) DO UPDATE SET created_at = now()`,
		sha, size, mime, l.blobs.Name, phash); err != nil {
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
