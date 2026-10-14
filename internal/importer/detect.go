package importer

import (
	"context"
	"errors"
	"fmt"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

// DetectKind is the job kind that records the sources of the entities
// imports queued. Its payload is empty.
const DetectKind = "detect-sources"

// detectPage is how many queued entities the job reads at a time.
const detectPage = 100

// RunDetect is the jobs.Handler for DetectKind. It drains the detection
// queue through the sites plugins. An entity a plugin failed on retryably
// stays queued, and the job is retried. With no sites plugin the whole queue
// stays, and the job ends.
func (im *Importer) RunDetect(ctx context.Context, r *jobs.Run) error {
	var detected int
	var retry error
	// Another pass picks up entities queued behind the cursor while one ran.
	for {
		done := 0
		retry = nil
		for after := (library.DetectItem{}); ; {
			page, err := im.Lib.DetectQueue(ctx, after, detectPage)
			if err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			after = page[len(page)-1]
			for _, it := range page {
				srcs, err := im.detect(ctx, it)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// Creating, editing, or checking a sites instance runs the job again.
				if errors.Is(err, plugins.ErrNoSites) {
					return nil
				}
				if err != nil {
					retry = err
					continue
				}
				if err := im.Lib.Detected(ctx, it, srcs); err != nil {
					return err
				}
				done++
			}
			r.SetProgress(fmt.Appendf(nil, `{"detected": %d}`, detected+done))
		}
		detected += done
		if done == 0 {
			break
		}
	}
	if retry != nil {
		return fmt.Errorf("%w: %w", jobs.ErrRetry, retry)
	}
	return nil
}

// detect returns the sources in the entity's head revision; none for an entity that is gone.
func (im *Importer) detect(ctx context.Context, it library.DetectItem) ([]library.Source, error) {
	ents := im.Lib.Entities(it.Kind)
	ent, err := ents.Get(ctx, it.ID)
	if errors.Is(err, library.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rev, err := ents.Revision(ctx, ent.HeadRevisionID)
	if err != nil {
		return nil, err
	}
	return im.Registry.DetectSources(ctx, im.Plugins, it.Kind, rev.Data)
}
