package backup

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
// manifests. skipped reports a backup folder whose manifest couldn't be read.
func backups(dir string, t Type) (infos []Info, ms []Manifest, skipped bool, err error) {
	es, err := os.ReadDir(filepath.Join(dir, string(t)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	for _, e := range es {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, string(t)+"-") || strings.HasSuffix(name, ".part") {
			continue
		}
		folder := filepath.Join(dir, string(t), name)
		m, err := readManifest(folder)
		if err != nil {
			skipped = true
			continue
		}
		size, err := backupSize(dir, folder, m)
		if err != nil {
			return nil, nil, false, err
		}
		infos = append(infos, Info{Name: name, Type: t, CreatedAt: m.CreatedAt, Size: size})
		ms = append(ms, m)
	}
	// Folder names sort by time.
	slices.Reverse(infos)
	slices.Reverse(ms)
	return infos, ms, skipped, nil
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
// then removes shared blobs no remaining backup lists. Callers hold the backup
// lock, so no write is copying blobs.
func prune(dir string, t Type, keep int) error {
	infos, ms, skipped, err := backups(dir, t)
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
	// A damaged backup might still list blobs, so keep them all.
	if t != Assets || skipped {
		return nil
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
