package enrich

import (
	"context"
	"errors"
	"fmt"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// SweepKind is the hourly job that queues enrich runs for changed cards an
// instance missed.
const SweepKind = "enrich-sweep"

// Sweeper runs SweepKind jobs.
type Sweeper struct {
	Lib      *library.Library
	Registry *plugins.Registry
	Queue    *jobs.Queue
}

// Run is the jobs.Handler for SweepKind.
func (sw *Sweeper) Run(ctx context.Context, _ *jobs.Run) error { return sw.Sweep(ctx) }

// Sweep queues, for each usable instance that updates cards as they
// change, one run over the cards changed since that it hasn't scored or
// failed on. An instance with a run queued or running is left for the
// next sweep, so runs never stack; each takes at most the plugin's card cap.
func (sw *Sweeper) Sweep(ctx context.Context) error {
	insts, err := sw.Registry.InstancesWith(ctx, protocol.CapabilityEnrich)
	if err != nil {
		return fmt.Errorf("list enrich instances: %w", err)
	}
	manifests := sw.Registry.Manifests()
	var errs []error
	for _, inst := range insts {
		if !inst.Usable() || !inst.EnrichOnChange {
			continue
		}
		ids, err := sw.Lib.ChangedUnenriched(ctx, inst.ID, inst.EnrichOnChangeSince, MaxCards(manifests[inst.Plugin]))
		if err != nil {
			errs = append(errs, fmt.Errorf("find changed cards for instance %d: %w", inst.ID, err))
			continue
		}
		if len(ids) == 0 {
			continue
		}
		spec := jobs.Spec{Kind: Kind, InstanceID: &inst.ID, Payload: Payload{CardIDs: ids}}
		if _, err := sw.Queue.EnqueueOnceForInstance(ctx, spec); err != nil {
			errs = append(errs, fmt.Errorf("queue enrich on instance %d: %w", inst.ID, err))
		}
	}
	return errors.Join(errs...)
}
