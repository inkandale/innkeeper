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
// does nothing; the job runs again when one is set. The instance is read
// before each batch, so changing it mid-run takes effect.
func (rn *Runner) Run(ctx context.Context, r *jobs.Run) error {
	var inst, after int64
	done := 0
	for {
		cur, err := rn.instance(ctx)
		if err != nil {
			return fmt.Errorf("find embed instance: %w", err)
		}
		if cur == 0 {
			return nil
		}
		if cur != inst {
			inst, after = cur, 0
		}
		todo, err := rn.Lib.Unembedded(ctx, inst, after, batchSize)
		if err != nil || len(todo) == 0 {
			return err
		}
		saved, err := rn.embedBatch(ctx, r, inst, todo)
		if err != nil {
			return err
		}
		// Past the batch even if a card was skipped, so it can't stall the rest.
		after = todo[len(todo)-1].CardID
		done += saved
		r.SetProgress(fmt.Appendf(nil, `{"embedded": %d}`, done))
	}
}

// embedBatch embeds cards and returns how many it saved. If the batch fails
// in a way one card could cause, such as the model rejecting a text, it tries
// each card alone and logs and skips the ones that still fail; they stay due
// for the next run. If no card gets through alone, the fault isn't one
// card's, so the batch's error is returned.
func (rn *Runner) embedBatch(ctx context.Context, r *jobs.Run, inst int64, cards []library.EmbedText) (int, error) {
	batchErr := rn.embed(ctx, inst, cards)
	if batchErr == nil {
		return len(cards), nil
	}
	if !cardFault(ctx, batchErr) {
		return 0, batchErr
	}
	if len(cards) == 1 {
		r.Log("warn", fmt.Sprintf("card %d not embedded: %v", cards[0].CardID, batchErr))
		return 0, nil
	}
	saved := 0
	for _, c := range cards {
		err := rn.embed(ctx, inst, []library.EmbedText{c})
		switch {
		case err == nil:
			saved++
		case cardFault(ctx, err):
			r.Log("warn", fmt.Sprintf("card %d not embedded: %v", c.CardID, err))
		default:
			return saved, err
		}
	}
	if saved == 0 {
		return 0, batchErr
	}
	return saved, nil
}

// embed asks the embed instance for cards' vectors and saves them.
func (rn *Runner) embed(ctx context.Context, inst int64, cards []library.EmbedText) error {
	texts := make([]string, len(cards))
	for i, c := range cards {
		texts[i] = c.Text
	}
	vectors, err := rn.Registry.Embed(ctx, rn.Plugins, inst, texts)
	if err != nil {
		return fmt.Errorf("embed %d cards: %w", len(cards), plugins.JobErr(err))
	}
	return rn.Lib.SaveEmbeddings(ctx, inst, cards, vectors)
}

// cardFault reports whether err may be one card's fault rather than the
// instance's, the plugin's, the connection's, or a cancelled job's.
func cardFault(ctx context.Context, err error) bool {
	var startErr *pluginhost.StartError
	return ctx.Err() == nil && !errors.Is(err, jobs.ErrRetry) && !errors.As(err, &startErr) &&
		!errors.Is(err, plugins.ErrBroken) && !errors.Is(err, plugins.ErrDisabled) && !errors.Is(err, plugins.ErrIncapable)
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
