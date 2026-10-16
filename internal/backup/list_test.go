package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLatest(t *testing.T) {
	dir := t.TempDir()
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	for _, name := range []string{
		"assets/assets-" + older.Format(timeFormat),
		"assets/assets-" + newer.Format(timeFormat),
		// Neither an unfinished write nor a stray folder counts.
		"assets/assets-" + newer.Add(time.Hour).Format(timeFormat) + ".part",
		"assets/assets-later",
		"assets/blobs",
	} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Latest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[Assets].Equal(newer) {
		t.Fatalf("Latest = %v, want assets at %v", got, newer)
	}
}
