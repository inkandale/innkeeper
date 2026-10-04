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
	// Dump first: no revision ever drops its avatar, so every blob the dump
	// refers to is still on disk when the copy runs.
	dump := filepath.Join(dir, "innkeeper-"+time.Now().UTC().Format("20060102T150405Z")+".dump")
	if err := pgDumpCommand(dbURL, dump).Run(); err != nil {
		return fmt.Errorf("pg_dump: %w", err)
	}
	n, err := copyBlobs(blobDir, filepath.Join(dir, "blobs"))
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s and copied %d new blob files\n", dump, n)
	return nil
}

// pgDumpCommand runs pg_dump on dbURL. The password goes in the environment,
// since other users can see a process's arguments.
func pgDumpCommand(dbURL, file string) *exec.Cmd {
	env := os.Environ()
	if u, err := url.Parse(dbURL); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok {
			env = append(env, "PGPASSWORD="+pw)
			u.User = url.User(u.User.Username())
			dbURL = u.String()
		}
	}
	cmd := exec.Command("pg_dump", "--format=custom", "--file="+file, "--dbname="+dbURL)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, os.Stderr, os.Stderr
	return cmd
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
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	part := dst + ".part"
	out, err := os.Create(part)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(part, dst)
}
