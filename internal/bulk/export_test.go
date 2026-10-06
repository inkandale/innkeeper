package bulk

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/export"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestExport(t *testing.T) {
	ctx := context.Background()
	lib, q, ids := setup(t, func(lib *library.Library) map[string]jobs.Handler {
		return map[string]jobs.Handler{ExportKind: Export(&export.Exporter{Lib: lib})}
	}, "a", "b")
	// A card deleted after the job was queued is skipped.
	j, err := EnqueueExport(ctx, q, append(ids, 99999), "json")
	if err != nil {
		t.Fatal(err)
	}
	j = wait(t, q, j.ID)
	var p ExportProgress
	if err := json.Unmarshal(j.Progress, &p); err != nil || p.Done != 3 || p.Total != 3 || p.Blob == "" {
		t.Fatalf("progress = %s, %v", j.Progress, err)
	}
	_, f, err := lib.OpenBlob(ctx, p.Blob)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	body, _ := io.ReadAll(f)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, zf := range zr.File {
		names = append(names, zf.Name)
	}
	if !slices.Equal(names, []string{"a.json", "b.json"}) {
		t.Fatalf("zip holds %v", names)
	}
}
