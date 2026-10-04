package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// backup runs `innkeeper backup DIR`: a database dump, plus a copy of every
// blob file DIR doesn't have yet. Blob files are named by their hash, so
// later runs copy only new ones.
func backup(args []string) error {
	if len(args) != 1 {
		return errors.New(usage)
	}
	dbURL, blobDir := os.Getenv("INNKEEPER_DATABASE_URL"), os.Getenv("INNKEEPER_BLOB_DIR")
	if dbURL == "" || blobDir == "" {
		return errors.New("INNKEEPER_DATABASE_URL and INNKEEPER_BLOB_DIR must be set")
	}
	dir := args[0]
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// MkdirAll leaves an existing directory's mode alone.
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if _, err := pgDumpCommand(dbURL, io.Discard); err != nil {
		return err
	}
	// Dump first: no revision ever drops its avatar, so every blob the dump
	// refers to is still on disk when the copy runs.
	dump, err := writeDump(dir, func(w io.Writer) *exec.Cmd {
		cmd, _ := pgDumpCommand(dbURL, w) // URL already checked above
		return cmd
	})
	if err != nil {
		return err
	}
	n, err := copyBlobs(blobDir, filepath.Join(dir, "blobs"))
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s and copied %d new blob files\n", dump, n)
	return nil
}

// writeDump runs mk's command into a new .part file and renames it only on
// success, so a failed or repeated run never replaces or fakes a backup.
func writeDump(dir string, mk func(io.Writer) *exec.Cmd) (string, error) {
	dump := filepath.Join(dir, "innkeeper-"+time.Now().UTC().Format("20060102T150405.000000Z")+".dump")
	part := dump + ".part"
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	err = mk(f).Run()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(part, dump)
	}
	if err != nil {
		os.Remove(part)
		return "", fmt.Errorf("pg_dump: %w", err)
	}
	return dump, nil
}

// pgDumpCommand dumps dbURL to out. The password goes in the environment,
// since other users can see a process's arguments.
func pgDumpCommand(dbURL string, out io.Writer) (*exec.Cmd, error) {
	u, err := url.Parse(dbURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, errors.New("backup needs INNKEEPER_DATABASE_URL as a postgres:// URL")
	}
	env := os.Environ()
	pw, ok := "", false
	if u.User != nil {
		pw, ok = u.User.Password()
		u.User = url.User(u.User.Username())
	}
	if q := u.Query(); q.Has("password") {
		pw, ok = q.Get("password"), true
		q.Del("password")
		u.RawQuery = q.Encode()
	}
	if ok {
		env = append(env, "PGPASSWORD="+pw)
	}
	cmd := exec.Command("pg_dump", "--format=custom", "--dbname="+u.String())
	cmd.Env, cmd.Stdout, cmd.Stderr = env, out, os.Stderr
	return cmd, nil
}

// copyBlobs copies blob files from src that dst lacks and returns how many
// it copied. It skips tmp and staging, which hold unfinished files.
func copyBlobs(src, dst string) (int, error) {
	n := 0
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel == "tmp" || rel == "staging" {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		if err := copyFile(path, target); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}

// copyFile writes to a temporary name first, so an interrupted copy never
// looks finished to the next run.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	part := dst + ".part"
	out, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(part, dst)
	}
	if err != nil {
		os.Remove(part)
	}
	return err
}
