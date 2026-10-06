package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/davidgibbons/innkeeper/internal/backup"
	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/secrets"
)

// backupEnv reads what both backup and restore need from the environment.
func backupEnv() (dbURL string, store *blobstore.Store, box *secrets.Box, err error) {
	dbURL, blobDir := os.Getenv("INNKEEPER_DATABASE_URL"), os.Getenv("INNKEEPER_BLOB_DIR")
	if dbURL == "" || blobDir == "" {
		return "", nil, nil, errors.New("INNKEEPER_DATABASE_URL and INNKEEPER_BLOB_DIR must be set")
	}
	if box, err = secrets.FromEnv(); err != nil {
		return "", nil, nil, err
	}
	if blobDir, err = filepath.Abs(blobDir); err != nil {
		return "", nil, nil, err
	}
	return dbURL, &blobstore.Store{Name: "library", Root: blobDir}, box, nil
}

// backupCmd runs `innkeeper backup DIR [assets|config]`.
func backupCmd(args []string) error {
	types := backup.Types
	switch len(args) {
	case 1:
	case 2:
		t := backup.Type(args[1])
		if !slices.Contains(backup.Types, t) {
			return fmt.Errorf("unknown backup type %q; use assets or config\n%s", args[1], usage)
		}
		types = []backup.Type{t}
	default:
		return errors.New(usage)
	}
	dbURL, store, box, err := backupEnv()
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := openDB(ctx, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	for _, t := range types {
		info, err := backup.Write(ctx, pool, t, backup.Options{Dir: args[0], DatabaseURL: dbURL, Store: store, Box: box})
		if err != nil {
			return fmt.Errorf("back up %s: %w", t, err)
		}
		fmt.Printf("wrote %s (%d bytes)\n", filepath.Join(args[0], string(info.Type), info.Name), info.Size)
	}
	return nil
}

type restoreItem struct {
	path string
	typ  backup.Type
}

// restoreOrder puts assets before config, which only keeps bindings whose
// cards exist, and otherwise keeps argument order.
func restoreOrder(items []restoreItem) []restoreItem {
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(a, b restoreItem) int {
		return slices.Index(backup.Types, a.typ) - slices.Index(backup.Types, b.typ)
	})
	return out
}

// restoreCmd runs `innkeeper restore [--replace] PATH...`.
func restoreCmd(args []string) error {
	replace := false
	var paths []string
	for _, a := range args {
		switch {
		case a == "--replace":
			replace = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q\n%s", a, usage)
		default:
			paths = append(paths, a)
		}
	}
	if len(paths) == 0 {
		return errors.New(usage)
	}
	items := make([]restoreItem, len(paths))
	for i, p := range paths {
		t, err := backupType(p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		items[i] = restoreItem{p, t}
	}
	_, store, box, err := backupEnv()
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := openDB(ctx, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	config := false
	for _, it := range restoreOrder(items) {
		err := withArchive(it.path, func(r io.Reader) error {
			res, err := backup.Restore(ctx, r, backup.RestoreOptions{Conn: pool.Config().ConnConfig, Store: store, Box: box, Replace: replace})
			if err != nil {
				return err
			}
			var rows int64
			for _, n := range res.Rows {
				rows += n
			}
			fmt.Printf("restored %s from %s: %d rows, %d blobs\n", res.Type, it.path, rows, res.Blobs)
			if len(res.SkippedBindings) > 0 {
				ids := make([]string, len(res.SkippedBindings))
				for i, id := range res.SkippedBindings {
					ids[i] = fmt.Sprint(id)
				}
				fmt.Printf("  skipped bindings whose card or lorebook isn't here: %s\n", strings.Join(ids, ", "))
			}
			if len(res.SkippedPluginSchemas) > 0 {
				fmt.Printf("  skipped plugin schemas with no plugin installed here: %s\n", strings.Join(res.SkippedPluginSchemas, ", "))
			}
			if res.SecretsKeyMismatch {
				fmt.Println("  warning: INNKEEPER_SECRETS_KEY differs from the one this backup was made with; plugin secrets won't open")
			}
			config = config || res.Type == backup.Config
			return nil
		})
		if errors.Is(err, backup.ErrHasData) {
			err = fmt.Errorf("%w (pass --replace to overwrite it)", err)
		}
		if err != nil {
			return fmt.Errorf("restore %s: %w", it.path, err)
		}
	}
	if config {
		fmt.Println("restart the api and worker so they repair plugin schemas")
	}
	return nil
}

// isFolder reports whether path is a backup folder rather than an archive file.
func isFolder(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// backupType reads the type from an archive's manifest or a folder's name.
func backupType(path string) (backup.Type, error) {
	if isFolder(path) {
		t := backup.Type(strings.SplitN(filepath.Base(filepath.Clean(path)), "-", 2)[0])
		if !slices.Contains(backup.Types, t) {
			return "", errors.New("not a backup folder; expected <dir>/<type>/<type>-<time>")
		}
		return t, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	m, err := backup.ReadManifest(f)
	return m.Type, err
}

// withArchive gives fn the archive at path, streaming a folder through a pipe.
func withArchive(path string, fn func(io.Reader) error) error {
	if !isFolder(path) {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return fn(f)
	}
	clean := filepath.Clean(path)
	root := filepath.Dir(filepath.Dir(clean))
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(backup.WriteArchive(pw, root, filepath.Base(clean))) }()
	err := fn(pr)
	pr.CloseWithError(err) // unblocks the writer if fn stopped early
	return err
}
