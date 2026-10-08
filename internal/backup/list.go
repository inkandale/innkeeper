package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func readManifest(folder string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(folder, "manifest.json"))
	if err == nil {
		err = json.Unmarshal(b, &m)
	}
	return m, err
}

// backupSize adds up folder's files and the shared blobs m lists.
func backupSize(dir, folder string, m Manifest) (int64, error) {
	var size int64
	err := filepath.WalkDir(folder, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		size += fi.Size()
		return nil
	})
	for _, sha := range m.Blobs {
		if fi, serr := os.Stat(blobPath(dir, sha)); serr == nil {
			size += fi.Size()
		}
	}
	return size, err
}

// backups returns the finished backups of type t, newest first, with their
// manifests. damaged names the backup folders whose manifest couldn't be read.
func backups(dir string, t Type) (infos []Info, ms []Manifest, damaged []string, err error) {
	es, err := os.ReadDir(filepath.Join(dir, string(t)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	for _, e := range es {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, string(t)+"-") || strings.HasSuffix(name, ".part") {
			continue
		}
		folder := filepath.Join(dir, string(t), name)
		m, err := readManifest(folder)
		if err != nil {
			damaged = append(damaged, name)
			continue
		}
		size, err := backupSize(dir, folder, m)
		if err != nil {
			return nil, nil, nil, err
		}
		infos = append(infos, Info{Name: name, Type: t, CreatedAt: m.CreatedAt, Size: size})
		ms = append(ms, m)
	}
	// Folder names sort by time.
	slices.Reverse(infos)
	slices.Reverse(ms)
	return infos, ms, damaged, nil
}

// List returns every finished backup, newest first.
func List(dir string) ([]Info, error) {
	var all []Info
	for _, t := range Types {
		infos, _, _, err := backups(dir, t)
		if err != nil {
			return nil, err
		}
		all = append(all, infos...)
	}
	slices.SortFunc(all, func(a, b Info) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return all, nil
}

// prune removes the backups of type t past the newest keep. For assets it
// then removes shared blobs no remaining backup lists, or returns an error if
// a damaged backup stops it. Callers hold the backup lock, so no write is
// copying blobs.
func prune(dir string, t Type, keep int) error {
	infos, ms, damaged, err := backups(dir, t)
	if err != nil {
		return err
	}
	listed := map[string]bool{}
	for i := range infos {
		if i < keep {
			for _, sha := range ms[i].Blobs {
				listed[sha] = true
			}
			continue
		}
		// Renamed first, so an interrupted removal is a .part folder the next
		// write sweeps up, never a backup missing files.
		folder := filepath.Join(dir, string(t), infos[i].Name)
		if err := os.Rename(folder, folder+".part"); err != nil {
			return err
		}
		if err := os.RemoveAll(folder + ".part"); err != nil {
			return err
		}
	}
	if t != Assets {
		return nil
	}
	if len(damaged) > 0 {
		// A damaged backup might still list blobs, so keep them all.
		return fmt.Errorf("kept every shared blob: %s has no readable manifest.json", strings.Join(damaged, ", "))
	}
	blobs := filepath.Join(dir, string(Assets), "blobs")
	return filepath.WalkDir(blobs, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == blobs {
			return fs.SkipAll
		}
		if err != nil || d.IsDir() || listed[d.Name()] {
			return err
		}
		return os.Remove(path)
	})
}

// Latest returns when each type's newest finished backup was made, read from
// folder names alone so it stays cheap enough for a health check.
func Latest(dir string) (map[Type]time.Time, error) {
	latest := map[Type]time.Time{}
	for _, t := range Types {
		es, err := os.ReadDir(filepath.Join(dir, string(t)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range es {
			stamp, ok := strings.CutPrefix(e.Name(), string(t)+"-")
			if !e.IsDir() || !ok {
				continue
			}
			// .part folders and stray names fail to parse.
			at, err := time.Parse(timeFormat, stamp)
			if err == nil && at.After(latest[t]) {
				latest[t] = at
			}
		}
	}
	return latest, nil
}
