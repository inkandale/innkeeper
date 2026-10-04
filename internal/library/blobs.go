package library

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/davidgibbons/innkeeper/internal/cardhash"
)

// Blob is a stored file.
type Blob struct {
	SHA256      string
	Size        int64
	MIME        string
	Store       string
	AvatarPhash *int64
	CreatedAt   time.Time
}

const blobCols = `sha256, size, mime, store, avatar_phash, created_at`

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
	sha, size, err := l.blobs.Ingest(path)
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
func (l *Library) record(ctx context.Context, sha string, size int64) (Blob, error) {
	if b, err := l.Blob(ctx, sha); err == nil {
		return b, nil
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
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (sha256) DO NOTHING`,
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
