// Package blobstore keeps files on disk by SHA-256, laid out as
// <root>/ab/cd/<sha256>. Plugins write new files to <root>/tmp/.
package blobstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

// ErrInvalid means Ingest rejected the file itself, not that the store failed.
var ErrInvalid = errors.New("invalid file")

var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Store is one blob directory. Name is recorded on each blob row.
type Store struct {
	Name string
	Root string
}

// TmpDir is where plugins write files for Ingest.
func (s *Store) TmpDir() string { return filepath.Join(s.Root, "tmp") }

func (s *Store) stagingDir() string { return filepath.Join(s.Root, "staging") }

// Path returns where the blob with this hash lives.
func (s *Store) Path(sha string) (string, error) {
	if !shaPattern.MatchString(sha) {
		return "", fmt.Errorf("invalid blob hash %q", sha)
	}
	return filepath.Join(s.Root, sha[0:2], sha[2:4], sha), nil
}

// ThumbPath returns where the w-pixel-wide thumbnail of a blob is cached.
func (s *Store) ThumbPath(sha string, w int) (string, error) {
	if !shaPattern.MatchString(sha) {
		return "", fmt.Errorf("invalid blob hash %q", sha)
	}
	return filepath.Join(s.Root, "thumbs", strconv.Itoa(w), sha[0:2], sha+".jpg"), nil
}

// WriteThumb writes a thumbnail to dst, a ThumbPath, all at once, so a
// concurrent reader never sees half a file. It writes in TmpDir, so
// RemoveStale sweeps up a crashed write.
func (s *Store) WriteThumb(dst string, write func(io.Writer) error) error {
	for _, dir := range []string{filepath.Dir(dst), s.TmpDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.CreateTemp(s.TmpDir(), "thumb-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = write(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), dst)
}

// Put copies r into the store and returns the blob's hash and size.
func (s *Store) Put(r io.Reader) (sha string, size int64, err error) {
	f, err := s.stage()
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(f, h), r)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	sha = hex.EncodeToString(h.Sum(nil))
	return sha, size, s.place(f.Name(), sha)
}

// Ingest moves a file a plugin wrote into the store. path is untrusted: it
// must name a regular file directly inside TmpDir, and the hash is computed
// here, never taken from the plugin.
//
// ponytail: a plugin still holding the file open could change it after it is
// hashed. Plugins run as the same OS user and could write the store directly
// anyway; copy instead of moving if plugins ever get their own user.
func (s *Store) Ingest(path string) (sha string, size int64, err error) {
	tmp, err := filepath.EvalSymlinks(s.TmpDir())
	if err != nil {
		return "", 0, err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", 0, fileErr(err)
	}
	if dir != tmp {
		return "", 0, fmt.Errorf("%w: %s is not inside %s", ErrInvalid, path, s.TmpDir())
	}
	// Move it out of the plugin-writable folder before reading it.
	staged, err := s.stage()
	if err != nil {
		return "", 0, err
	}
	staged.Close()
	defer os.Remove(staged.Name())
	if err := os.Rename(filepath.Join(dir, filepath.Base(path)), staged.Name()); err != nil {
		return "", 0, fileErr(err)
	}
	f, err := os.OpenFile(staged.Name(), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", 0, fmt.Errorf("open %s: %w", path, fileErr(err))
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil {
		return "", 0, err
	} else if !fi.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%w: %s is not a regular file", ErrInvalid, path)
	} else if st, ok := fi.Sys().(*syscall.Stat_t); !ok || uint64(st.Nlink) != 1 {
		// A hard link would let place chmod some other file.
		return "", 0, fmt.Errorf("%w: %s has other hard links", ErrInvalid, path)
	}
	h := sha256.New()
	if size, err = io.Copy(h, f); err != nil {
		return "", 0, err
	}
	sha = hex.EncodeToString(h.Sum(nil))
	return sha, size, s.place(staged.Name(), sha)
}

// fileErr marks errors from a missing path, a directory, or a symlink as
// ErrInvalid, and leaves others, such as a full disk, as they are.
func fileErr(err error) error {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP) {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return err
}

func (s *Store) stage() (*os.File, error) {
	if err := os.MkdirAll(s.stagingDir(), 0o755); err != nil {
		return nil, err
	}
	return os.CreateTemp(s.stagingDir(), "blob-*")
}

// place renames a staged file to its final path. If the blob already exists
// the rename replaces it with identical bytes, so concurrent puts are safe.
func (s *Store) place(staged, sha string) error {
	dst, err := s.Path(sha)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(staged, 0o444); err != nil {
		return err
	}
	return os.Rename(staged, dst)
}

// Remove deletes a blob's file and its thumbnails. A file that is already
// gone is not an error.
func (s *Store) Remove(sha string) error {
	path, err := s.Path(sha)
	if err != nil {
		return err
	}
	paths := []string{path}
	widths, err := os.ReadDir(filepath.Join(s.Root, "thumbs"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, w := range widths {
		paths = append(paths, filepath.Join(s.Root, "thumbs", w.Name(), sha[0:2], sha+".jpg"))
	}
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// RemoveStale deletes entries in the tmp and staging folders last modified
// before cutoff: files from plugins and puts that never finished.
func (s *Store) RemoveStale(cutoff time.Time) (int, error) {
	n := 0
	for _, dir := range []string{s.TmpDir(), s.stagingDir()} {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return n, err
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil || !info.ModTime().Before(cutoff) {
				continue
			}
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}
