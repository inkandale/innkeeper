// Package libsettings reads the settings a library can override: the
// library's own value, else the core's. It also turns a library's hide
// settings into a hide job's payload.
package libsettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// ScoreInstanceKey names the enrich instance whose values a library's grid shows.
const ScoreInstanceKey = "score_instance"

var (
	// ErrNothingToHide means a library's settings hide no cards.
	ErrNothingToHide = errors.New("nothing to hide: set hide_below or duplicates")
	// ErrNoGridScore means hide_below is set but no score instance shows a score on the grid.
	ErrNoGridScore = errors.New("hide_below needs a score instance whose plugin shows a score on the grid")
)

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

// ScoreNamed is the enrich instance a score_instance setting's value names,
// or nil: a deleted instance, or one whose plugin no longer enriches, reads
// as unset.
func ScoreNamed(v string, insts []plugins.InstanceInfo) *plugins.InstanceInfo {
	id, err := strconv.ParseInt(v, 10, 64)
	if i := slices.IndexFunc(insts, func(i plugins.InstanceInfo) bool { return i.ID == id }); err == nil && i >= 0 {
		return &insts[i]
	}
	return nil
}

// ScoreInstance is the enrich instance whose values library lib's grid
// shows and unnamed filters read: the score_instance setting while it names
// an enrich instance, else the only enrich instance, else nil.
func (s *Settings) ScoreInstance(ctx context.Context, lib int64) (*plugins.InstanceInfo, error) {
	v, _, err := s.Get(ctx, lib, ScoreInstanceKey)
	if err != nil {
		return nil, err
	}
	insts, err := s.Enrichers(ctx)
	if err != nil {
		return nil, err
	}
	if set := ScoreNamed(v, insts); set != nil {
		return set, nil
	}
	if len(insts) == 1 {
		return &insts[0], nil
	}
	return nil, nil
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
	HideBelow = Option[float64]{Key: "hide_below", Check: func(v float64) error {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("want a number")
		}
		return nil
	}}
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
// It fails with ErrNothingToHide or ErrNoGridScore when the settings can't
// run, and with the registry's error when duplicate_decide names an instance
// that can't decide.
func (s *Settings) HidePayload(ctx context.Context, lib int64) (bulk.HidePayload, error) {
	p := bulk.HidePayload{Library: lib}
	var err error
	if p.HideBelow, _, _, err = HideBelow.Value(ctx, s, lib); err != nil {
		return p, err
	}
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
	if p.HideBelow == nil && p.Duplicates == "off" {
		return p, ErrNothingToHide
	}
	inst, err := s.ScoreInstance(ctx, lib)
	if err != nil {
		return p, err
	}
	if inst != nil {
		for k, d := range s.reg.Manifests()[inst.Plugin].Enrichments {
			if d.Grid {
				p.ScoreInstance, p.ScoreKey = inst.ID, k
			}
		}
	}
	if p.HideBelow != nil && p.ScoreKey == "" {
		return p, ErrNoGridScore
	}
	return p, nil
}

// EnqueueHide queues the hide job for library lib when its settings hide
// duplicates, so cards that just arrived don't sit beside their copies.
// Hiding by score alone queues nothing, since arrivals change no one's score.
func (s *Settings) EnqueueHide(ctx context.Context, q *jobs.Queue, lib int64) error {
	// The caller's change has committed, so a client leaving mustn't stop the job.
	ctx = context.WithoutCancel(ctx)
	p, err := s.HidePayload(ctx, lib)
	if errors.Is(err, ErrNothingToHide) || err == nil && p.Duplicates == "off" {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = q.Enqueue(ctx, jobs.Spec{Kind: bulk.HideKind, Payload: p})
	return err
}
