package libsettings

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/enrich"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
)

// Gate is the enrich.Gate. An instance gates a library when an enabled
// archive rule covering it reads the instance and the instance runs as cards
// change; a card there that the gate's sweep will reach waits for each gate
// cheaper than the instance it's sent to, until the gate has a value, vector
// or failure for its head revision. An instance that runs only when the
// owner starts it gates nothing, so no card waits for it forever.
type Gate struct {
	Settings *Settings
	Queue    *jobs.Queue
}

// gating is what Gate reads about the enabled enrich instances and rules.
type gating struct {
	insts     []plugins.InstanceInfo
	cost      map[int64]int
	auto      map[int64]bool // runs as cards change, so it can gate
	since     map[int64]time.Time
	browsable map[string][]library.Scorer
	rules     []library.Rule // enabled only
}

func (g *Gate) load(ctx context.Context) (gating, error) {
	s := g.Settings
	insts, err := s.Enrichers(ctx)
	if err != nil {
		return gating{}, err
	}
	manifests := s.reg.Manifests()
	cost, auto, since := map[int64]int{}, map[int64]bool{}, map[int64]time.Time{}
	for _, inst := range insts {
		if m := manifests[inst.Plugin]; m != nil {
			cost[inst.ID] = m.EnrichCost
		}
		auto[inst.ID] = inst.EnrichOnChange && inst.Usable()
		since[inst.ID] = inst.EnrichOnChangeSince
	}
	browsable, err := s.Browsable(ctx)
	if err != nil {
		return gating{}, err
	}
	rules, err := s.lib.Rules(ctx)
	if err != nil {
		return gating{}, err
	}
	rules = slices.DeleteFunc(rules, func(r library.Rule) bool { return !r.Enabled })
	return gating{insts, cost, auto, since, browsable, rules}, nil
}

// gates returns the instances cheaper than instance that gate libraryID.
func (gt gating) gates(instance, libraryID int64) []int64 {
	own, ok := gt.cost[instance]
	if !ok {
		return nil
	}
	var out []int64
	for _, r := range gt.rules {
		if r.Action != library.RuleArchive || !r.Covers(libraryID) {
			continue
		}
		for _, id := range library.ReadInstances(r.Filter, gt.browsable) {
			if c, ok := gt.cost[id]; ok && c < own && gt.auto[id] {
				out = append(out, id)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// reads reports whether a rule that keep accepts reads instance.
func (gt gating) reads(instance int64, keep func(library.Rule) bool) bool {
	return slices.ContainsFunc(gt.rules, func(r library.Rule) bool {
		return keep(r) && slices.Contains(library.ReadInstances(r.Filter, gt.browsable), instance)
	})
}

// Waiting returns which of cardIDs instance must not be sent yet.
func (g *Gate) Waiting(ctx context.Context, instance int64, cardIDs []int64) ([]int64, error) {
	gt, err := g.load(ctx)
	if err != nil || len(gt.rules) == 0 {
		return nil, err
	}
	libs, err := g.Settings.lib.CardLibraries(ctx, cardIDs)
	if err != nil {
		return nil, err
	}
	byGate := map[int64][]int64{}
	for _, card := range cardIDs {
		if lib, ok := libs[card]; ok {
			for _, gate := range gt.gates(instance, lib) {
				byGate[gate] = append(byGate[gate], card)
			}
		}
	}
	var waiting []int64
	for gate, cards := range byGate {
		pending, err := g.Settings.lib.Unanswered(ctx, gate, gt.since[gate], cards)
		if err != nil {
			return nil, fmt.Errorf("find cards instance %d hasn't answered: %w", gate, err)
		}
		waiting = append(waiting, pending...)
	}
	slices.Sort(waiting)
	return slices.Compact(waiting), nil
}

// Answered applies the rules to cardIDs once instance, which an enabled rule
// reads, has answered them. It then queues the cards still unarchived on
// each instance that instance gates in their library; one that didn't wait
// skips the cards it already scored.
func (g *Gate) Answered(ctx context.Context, instance int64, cardIDs []int64) error {
	gt, err := g.load(ctx)
	if err != nil || !gt.reads(instance, func(library.Rule) bool { return true }) {
		return err
	}
	lib := g.Settings.lib
	libs, err := lib.CardLibraries(ctx, cardIDs)
	if err != nil {
		return err
	}
	byLib := map[int64][]int64{}
	for card, l := range libs {
		byLib[l] = append(byLib[l], card)
	}
	for _, l := range slices.Sorted(maps.Keys(byLib)) {
		among := byLib[l]
		slices.Sort(among)
		// Duplicates aren't recomputed here, so the ones already archived stay.
		dups, err := lib.ArchivedAs(ctx, among, library.ReasonDuplicate)
		if err != nil {
			return err
		}
		var prog bulk.HideProgress
		if err := bulk.ApplyRules(ctx, lib, l, gt.browsable, dups, among, &prog); err != nil {
			return fmt.Errorf("apply rules in library %d: %w", l, err)
		}
	}
	if !gt.auto[instance] {
		return nil
	}
	live, err := lib.Unarchived(ctx, cardIDs)
	if err != nil || len(live) == 0 {
		return err
	}
	manifests := g.Settings.reg.Manifests()
	var errs []error
	for _, x := range gt.insts {
		gated := slices.DeleteFunc(slices.Clone(live), func(card int64) bool {
			return !gt.auto[x.ID] || !slices.Contains(gt.gates(x.ID, libs[card]), instance)
		})
		if len(gated) == 0 {
			continue
		}
		p := enrich.Payload{CardIDs: gated[:min(len(gated), enrich.MaxCards(manifests[x.Plugin]))], Wait: true}
		if _, err := g.Queue.EnqueueUnlessQueued(ctx, jobs.Spec{Kind: enrich.Kind, InstanceID: &x.ID, Payload: p}); err != nil {
			errs = append(errs, fmt.Errorf("queue enrich on instance %d: %w", x.ID, err))
		}
	}
	return errors.Join(errs...)
}
