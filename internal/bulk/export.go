package bulk

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/davidgibbons/innkeeper/internal/export"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
)

// ExportKind writes cards into one zip blob.
const ExportKind = "card.export"

type exportPayload struct {
	CardIDs []int64 `json:"card_ids"`
	Format  string  `json:"format"`
}

// ExportProgress is an export job's progress. Blob is the zip's SHA-256,
// set once the zip is stored.
type ExportProgress struct {
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Blob  string `json:"blob,omitempty"`
}

// EnqueueExport queues one job that writes the cards in format to a zip.
func EnqueueExport(ctx context.Context, q *jobs.Queue, cardIDs []int64, format string) (jobs.Job, error) {
	return q.Enqueue(ctx, jobs.Spec{Kind: ExportKind, Payload: exportPayload{CardIDs: cardIDs, Format: format}})
}

// Export is the jobs.Handler for ExportKind. Nothing references the zip, so
// blob cleanup deletes it after a day.
func Export(ex *export.Exporter) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p exportPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		tmp, err := os.CreateTemp("", "innkeeper-export-*.zip")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		zw := zip.NewWriter(tmp)
		progress := func(pr ExportProgress) {
			b, _ := json.Marshal(pr)
			r.SetProgress(b)
		}
		for i, id := range p.CardIDs {
			if err := ctx.Err(); err != nil {
				return err
			}
			f, err := ex.Card(ctx, id, p.Format)
			if errors.Is(err, library.ErrNotFound) {
				progress(ExportProgress{Done: i + 1, Total: len(p.CardIDs)})
				continue
			}
			if err != nil {
				return fmt.Errorf("card %d: %w", id, err)
			}
			w, err := zw.Create(f.Name)
			if err != nil {
				return err
			}
			if _, err := w.Write(f.Body); err != nil {
				return err
			}
			progress(ExportProgress{Done: i + 1, Total: len(p.CardIDs)})
		}
		if err := zw.Close(); err != nil {
			return err
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return err
		}
		b, err := ex.Lib.PutBlob(ctx, tmp)
		if err != nil {
			return err
		}
		progress(ExportProgress{Done: len(p.CardIDs), Total: len(p.CardIDs), Blob: b.SHA256})
		return nil
	}
}
