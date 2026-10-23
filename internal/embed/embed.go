// Package embed keeps every card's vector from the embed instance current,
// so cards can be ranked by meaning.
package embed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
)

// Kind is the job kind that embeds cards whose vector is missing or stale.
const Kind = "embed"

// InstanceKey is the setting naming the ai instance whose ai.embed every
// library's vectors come from. One instance for all libraries, since
// vectors from different models can't be compared.
const InstanceKey = "embed_instance"

// batchSize is how many texts go in one ai.embed call.
const batchSize = 32

// Runner is the jobs.Handler for Kind.
type Runner struct {
	Lib      *library.Library
	Registry *plugins.Registry
	Plugins  *pluginhost.Manager
}

// Run embeds batches until no card is due. With no usable embed instance it
// does nothing; the job runs again when one is set.
func (rn *Runner) Run(ctx context.Context, r *jobs.Run) error {
	inst, err := rn.instance(ctx)
	if err != nil || inst == 0 {
		return err
	}
	done := 0
	for {
		todo, err := rn.Lib.Unembedded(ctx, inst, batchSize)
		if err != nil || len(todo) == 0 {
			return err
		}
		texts := make([]string, len(todo))
		for i, c := range todo {
			texts[i] = c.Text
		}
		vectors, err := rn.Registry.Embed(ctx, rn.Plugins, inst, texts)
		if err != nil {
			return fmt.Errorf("embed %d cards: %w", len(todo), err)
		}
		if err := rn.Lib.SaveEmbeddings(ctx, inst, todo, vectors); err != nil {
			return err
		}
		done += len(todo)
		r.SetProgress(fmt.Appendf(nil, `{"embedded": %d}`, done))
	}
}

// instance is the embed instance's ID, or 0 when the setting is empty or
// names an instance that is gone or disabled.
func (rn *Runner) instance(ctx context.Context) (int64, error) {
	v, err := rn.Registry.Setting(ctx, InstanceKey)
	if err != nil || v == "" {
		return 0, err
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("setting %s = %q: %w", InstanceKey, v, err)
	}
	info, err := rn.Registry.GetInstance(ctx, id)
	if errors.Is(err, plugins.ErrNotFound) || (err == nil && info.Disabled) {
		return 0, nil
	}
	return id, err
}

// Enqueue makes sure an embed job will run. A failure is only logged; the
// hourly run catches up.
func Enqueue(ctx context.Context, q *jobs.Queue, log *slog.Logger) {
	if _, err := q.EnqueueOnce(context.WithoutCancel(ctx), jobs.Spec{Kind: Kind}); err != nil {
		log.Error("enqueue embedding", "err", err)
	}
}
