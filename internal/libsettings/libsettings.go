// Package libsettings reads the settings a library can override: the
// library's own value, else the core's. It also turns a library's hide
// settings into a hide job's payload, queues the enrich jobs an import's
// new cards start, and holds back cards for a cheaper enrich instance.
package libsettings

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/enrich"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// ErrNothingToHide means a library's settings hide no duplicates and no rule has work there.
var ErrNothingToHide = errors.New("nothing to hide: set duplicates or add a rule")

// Settings reads library and core settings.
type Settings struct {
	lib *library.Library
	reg *plugins.Registry
}

func New(lib *library.Library, reg *plugins.Registry) *Settings {
	return &Settings{lib: lib, reg: reg}
}

// Get returns library lib's value of key, else the core's, and whether lib overrides it.
func (s *Settings) Get(ctx context.Context, lib int64, key string) (string, bool, error) {
	v, err := s.lib.LibrarySetting(ctx, lib, key)
	if v != "" || err != nil {
		return v, true, err
	}
	v, err = s.reg.Setting(ctx, key)
	return v, false, err
}

// Enrichers is the enabled enrich instances.
func (s *Settings) Enrichers(ctx context.Context) ([]plugins.InstanceInfo, error) {
	insts, err := s.reg.InstancesWith(ctx, protocol.CapabilityEnrich)
	return slices.DeleteFunc(insts, func(i plugins.InstanceInfo) bool { return i.Disabled }), err
}

// declared is a key an enabled enrich instance's plugin declares.
type declared struct {
	library.Scorer
	protocol.Enrichment
}

// declarations are the enabled enrich instances' keys that keep accepts,
// highest priority first, then oldest instance first.
func (s *Settings) declarations(ctx context.Context, keep func(m *protocol.Manifest, key string) bool) ([]declared, error) {
	insts, err := s.Enrichers(ctx)
	if err != nil {
		return nil, err
	}
	manifests := s.reg.Manifests()
	var all []declared
	for _, inst := range insts {
		m := manifests[inst.Plugin]
		for k, d := range m.Enrichments {
			if keep(m, k) {
				all = append(all, declared{library.Scorer{Instance: inst.ID, Key: k}, d})
			}
		}
	}
	slices.SortFunc(all, func(a, b declared) int {
		return cmp.Or(cmp.Compare(b.Priority, a.Priority), cmp.Compare(a.Instance, b.Instance), cmp.Compare(a.Key, b.Key))
	})
	return all, nil
}

// Scorers are the enabled enrich instances' grid keys, highest priority
// first, then oldest instance first.
func (s *Settings) Scorers(ctx context.Context) ([]library.Scorer, error) {
	all, err := s.declarations(ctx, func(m *protocol.Manifest, key string) bool { return m.Enrichments[key].Grid })
	if err != nil {
		return nil, err
	}
	out := make([]library.Scorer, len(all))
	for i, d := range all {
		out[i] = d.Scorer
	}
	return out, nil
}

// BrowseKey is one entry of the filter sidebar and sort menu.
type BrowseKey struct {
	Key string
	// Instance is 0 for a key merged across the instances that declare it
	// browsable. A key they declare with different types doesn't merge: it
	// gets one BrowseKey per instance, naming it.
	Instance int64
	// Enrichment is the highest-priority instance's declaration.
	protocol.Enrichment
	// Scorers are the instances the key reads, highest priority first.
	Scorers []library.Scorer
}

// BrowseKeys are the enabled enrich instances' browsable keys, merged by
// name, in the order of each key's highest-priority instance.
func (s *Settings) BrowseKeys(ctx context.Context) ([]BrowseKey, error) {
	all, err := s.declarations(ctx, (*protocol.Manifest).Browsable)
	if err != nil {
		return nil, err
	}
	var names []string
	byName := map[string][]declared{}
	for _, d := range all {
		if byName[d.Key] == nil {
			names = append(names, d.Key)
		}
		byName[d.Key] = append(byName[d.Key], d)
	}
	var out []BrowseKey
	for _, name := range names {
		ds := byName[name]
		if slices.ContainsFunc(ds, func(d declared) bool { return d.Type != ds[0].Type }) {
			for _, d := range ds {
				out = append(out, BrowseKey{Key: name, Instance: d.Instance, Enrichment: d.Enrichment, Scorers: []library.Scorer{d.Scorer}})
			}
			continue
		}
		merged := BrowseKey{Key: name, Enrichment: ds[0].Enrichment}
		for _, d := range ds {
			merged.Scorers = append(merged.Scorers, d.Scorer)
		}
		out = append(out, merged)
	}
	return out, nil
}

// Browsable is Query.Browsable: each merged browsable key's scorers,
// highest priority first. A key declared with two types doesn't merge, so
// it isn't here, and a term naming it without an instance is refused.
func (s *Settings) Browsable(ctx context.Context) (map[string][]library.Scorer, error) {
	keys, err := s.BrowseKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]library.Scorer{}
	for _, k := range keys {
		if k.Instance == 0 {
			out[k.Key] = k.Scorers
		}
	}
	return out, nil
}

// Option is a setting a library can override, stored as its JSON encoding.
// Def is its value when neither the library nor the core sets one.
type Option[T comparable] struct {
	Key   string
	Def   *T
	Check func(T) error
}

// Decode reads a stored value, or Def for an empty or unreadable one.
func (o Option[T]) Decode(v string) *T {
	var t T
	if v == "" || json.Unmarshal([]byte(v), &t) != nil {
		return o.Def
	}
	return &t
}

// Core is the core's value: the default every library without an override follows.
func (o Option[T]) Core(ctx context.Context, s *Settings) (*T, error) {
	v, err := s.reg.Setting(ctx, o.Key)
	return o.Decode(v), err
}

// Value is library lib's value, the core's value, and whether lib overrides it.
func (o Option[T]) Value(ctx context.Context, s *Settings, lib int64) (value, core *T, overridden bool, err error) {
	if core, err = o.Core(ctx, s); err != nil {
		return nil, nil, false, err
	}
	own, err := s.lib.LibrarySetting(ctx, lib, o.Key)
	if own == "" {
		return core, core, false, err
	}
	return o.Decode(own), core, true, err
}

func oneOf(values ...string) func(string) error {
	return func(v string) error {
		if !slices.Contains(values, v) {
			return fmt.Errorf("want one of %v, not %q", values, v)
		}
		return nil
	}
}

// The hide job's settings.
var (
	Duplicates        = Option[string]{Key: "duplicates", Def: new("off"), Check: oneOf("off", "exact", "near")}
	DuplicateDistance = Option[int]{Key: "duplicate_distance", Def: new(3), Check: func(v int) error {
		if v < 0 || v > library.NearDistance {
			return fmt.Errorf("want 0 to %d", library.NearDistance)
		}
		return nil
	}}
	DuplicateKeep   = Option[string]{Key: "duplicate_keep", Def: new("best"), Check: oneOf("best", "oldest", "newest")}
	DuplicateDecide = Option[int64]{Key: "duplicate_decide", Check: func(v int64) error {
		if v <= 0 {
			return fmt.Errorf("want an ai instance's ID")
		}
		return nil
	}}
)

// HidePayload is the hide job's payload for library lib from its settings.
// It fails with ErrNothingToHide when the settings hide no duplicates and no
// rule has work in lib, and with the registry's error when duplicate_decide
// names an instance that can't decide.
func (s *Settings) HidePayload(ctx context.Context, lib int64) (bulk.HidePayload, error) {
	p, err := s.payload(ctx, lib)
	if err != nil || p.Duplicates != "off" {
		return p, err
	}
	work, err := s.lib.HasRuleWork(ctx, lib)
	if err == nil && !work {
		err = ErrNothingToHide
	}
	return p, err
}

// payload is the hide job's payload for library lib from its settings.
func (s *Settings) payload(ctx context.Context, lib int64) (bulk.HidePayload, error) {
	p := bulk.HidePayload{Library: lib}
	var err error
	var dups, keep *string
	var dist *int
	var decide *int64
	if dups, _, _, err = Duplicates.Value(ctx, s, lib); err != nil {
		return p, err
	}
	if dist, _, _, err = DuplicateDistance.Value(ctx, s, lib); err != nil {
		return p, err
	}
	if keep, _, _, err = DuplicateKeep.Value(ctx, s, lib); err != nil {
		return p, err
	}
	if decide, _, _, err = DuplicateDecide.Value(ctx, s, lib); err != nil {
		return p, err
	}
	p.Duplicates, p.Distance, p.Keep = *dups, *dist, *keep
	if decide != nil && p.Duplicates == "near" {
		if _, err := s.reg.InstanceWith(ctx, *decide, protocol.CapabilityAI); err != nil {
			return p, err
		}
		p.Decide = *decide
	}
	if p.Scorers, err = s.Scorers(ctx); err != nil {
		return p, err
	}
	p.Browsable, err = s.Browsable(ctx)
	return p, err
}

// EnqueueHide queues the hide job for library lib when its settings hide
// duplicates, so cards that just arrived don't sit beside their copies.
// It joins a hide job already queued, so a run of arrivals queues one.
func (s *Settings) EnqueueHide(ctx context.Context, q *jobs.Queue, lib int64) error {
	// The caller's change has committed, so a client leaving mustn't stop the job.
	ctx = context.WithoutCancel(ctx)
	p, err := s.HidePayload(ctx, lib)
	if errors.Is(err, ErrNothingToHide) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = q.EnqueueUnlessQueued(ctx, jobs.Spec{Kind: bulk.HideKind, Payload: p})
	return err
}

// RecheckHide queues the hide job for library lib even when nothing there is
// hidden yet, so a rule's change is applied there or undone. It joins a hide
// job already queued.
func (s *Settings) RecheckHide(ctx context.Context, q *jobs.Queue, lib int64) error {
	ctx = context.WithoutCancel(ctx)
	p, err := s.payload(ctx, lib)
	if err != nil {
		return err
	}
	_, err = q.EnqueueUnlessQueued(ctx, jobs.Spec{Kind: bulk.HideKind, Payload: p})
	return err
}

// EnqueueEnrichOnChange queues one enrich job for cardIDs, which an import
// or an edit just changed, on each enabled instance that updates cards as
// they change. A job takes at most enrich.MaxCards of them; the sweep picks
// up the rest.
func (s *Settings) EnqueueEnrichOnChange(ctx context.Context, q *jobs.Queue, cardIDs []int64) error {
	ctx = context.WithoutCancel(ctx)
	insts, err := s.Enrichers(ctx)
	if err != nil {
		return err
	}
	ids := slices.Compact(slices.Sorted(slices.Values(cardIDs)))
	manifests := s.reg.Manifests()
	var errs []error
	for _, inst := range insts {
		if !inst.EnrichOnChange {
			continue
		}
		p := enrich.Payload{CardIDs: ids[:min(len(ids), enrich.MaxCards(manifests[inst.Plugin]))], Wait: true}
		// Joins a run still queued for the same cards, so quick repeated saves cost one call; it reads the newest revision when it runs.
		if _, err := q.EnqueueUnlessQueued(ctx, jobs.Spec{Kind: enrich.Kind, InstanceID: &inst.ID, Payload: p}); err != nil {
			errs = append(errs, fmt.Errorf("queue enrich on instance %d: %w", inst.ID, err))
		}
	}
	return errors.Join(errs...)
}
