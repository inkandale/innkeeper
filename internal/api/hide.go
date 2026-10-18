package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/tier"
)

// libraryOption is a setting a library can override, stored as its JSON
// encoding. def is its value when neither the library nor the core sets one.
type libraryOption[T comparable] struct {
	key   string
	def   *T
	check func(T) error
}

func (o libraryOption[T]) decode(v string) *T {
	var t T
	if v == "" || json.Unmarshal([]byte(v), &t) != nil {
		return o.def
	}
	return &t
}

// core is the core's value: the default every library without an override follows.
func (o libraryOption[T]) core(ctx context.Context, s *Server) (*T, error) {
	v, err := s.Registry.Setting(ctx, o.key)
	return o.decode(v), err
}

// out is library lib's value, the default, and whether lib overrides it.
func (o libraryOption[T]) out(ctx context.Context, s *Server, lib int64) (librarySettingOut[T], error) {
	def, err := o.core(ctx, s)
	if err != nil {
		return librarySettingOut[T]{}, err
	}
	own, err := s.Lib.LibrarySetting(ctx, lib, o.key)
	out := librarySettingOut[T]{Value: def, Default: def, Overridden: own != ""}
	if own != "" {
		out.Value = o.decode(own)
	}
	return out, err
}

// set stores v as the core's value for a nil lib and as library lib's
// override otherwise; nil clears it.
func (o libraryOption[T]) set(ctx context.Context, s *Server, lib *int64, v *T) error {
	text := ""
	if v != nil {
		if err := o.check(*v); err != nil {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("%s: %v", o.key, err))
		}
		b, err := json.Marshal(*v)
		if err != nil {
			return err
		}
		text = string(b)
	}
	if lib == nil {
		return s.fail(s.Registry.SetSetting(ctx, o.key, text))
	}
	return s.fail(s.Lib.SetLibrarySetting(ctx, *lib, o.key, text))
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
	hideBelow = libraryOption[float64]{key: "hide_below", check: func(v float64) error {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("want a number")
		}
		return nil
	}}
	duplicates        = libraryOption[string]{key: "duplicates", def: new("off"), check: oneOf("off", "exact", "near")}
	duplicateDistance = libraryOption[int]{key: "duplicate_distance", def: new(3), check: func(v int) error {
		if v < 0 || v > library.NearDistance {
			return fmt.Errorf("want 0 to %d", library.NearDistance)
		}
		return nil
	}}
	duplicateKeep   = libraryOption[string]{key: "duplicate_keep", def: new("best"), check: oneOf("best", "oldest", "newest")}
	duplicateDecide = libraryOption[int64]{key: "duplicate_decide", check: func(v int64) error {
		if v <= 0 {
			return fmt.Errorf("want an ai instance's ID")
		}
		return nil
	}}
)

// HideOptionsIn are the hide settings a settings PATCH may change, each a
// value or null.
type HideOptionsIn struct {
	HideBelow         optional[float64] `json:"hide_below,omitempty" doc:"Hide cards whose grid score is below this."`
	Duplicates        optional[string]  `json:"duplicates,omitempty" doc:"off, exact, or near."`
	DuplicateDistance optional[int]     `json:"duplicate_distance,omitempty" doc:"0 to 8."`
	DuplicateKeep     optional[string]  `json:"duplicate_keep,omitempty" doc:"best, oldest, or newest."`
	DuplicateDecide   optional[int64]   `json:"duplicate_decide,omitempty" doc:"An ai instance offering ai.decide, which confirms near duplicates."`
}

// set stores each sent setting, for library lib or, for a nil lib, the core.
func (in HideOptionsIn) set(ctx context.Context, s *Server, lib *int64) error {
	for _, f := range []func() error{
		func() error { return setSent(ctx, s, lib, hideBelow, in.HideBelow) },
		func() error { return setSent(ctx, s, lib, duplicates, in.Duplicates) },
		func() error { return setSent(ctx, s, lib, duplicateDistance, in.DuplicateDistance) },
		func() error { return setSent(ctx, s, lib, duplicateKeep, in.DuplicateKeep) },
		func() error { return setSent(ctx, s, lib, duplicateDecide, in.DuplicateDecide) },
	} {
		if err := f(); err != nil {
			return err
		}
	}
	return nil
}

func setSent[T comparable](ctx context.Context, s *Server, lib *int64, o libraryOption[T], v optional[T]) error {
	if !v.Sent {
		return nil
	}
	return o.set(ctx, s, lib, v.Value)
}

type hideIn struct {
	ID int64 `path:"id"`
}

func (s *Server) hideRoutes(api huma.API) {
	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "hide-cards", Method: http.MethodPost,
		Path: "/libraries/{id}/hide", Summary: "Archive a library's low-scoring and duplicate cards", Tags: []string{"Libraries"},
		Description: "Queues a job that archives, by the library's settings, each card scoring below hide_below and every " +
			"duplicate but the one duplicate_keep picks, and records why. Cards it archived on an earlier run that now pass " +
			"are unarchived. Cards the owner archived are left alone. Its progress counts each.",
		DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *hideIn) (*jobResp, error) {
			set, err := s.librarySettings(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			b := set.Body
			p := bulk.HidePayload{Library: in.ID, HideBelow: b.HideBelow.Value, Duplicates: *b.Duplicates.Value,
				Distance: *b.DuplicateDistance.Value, Keep: *b.DuplicateKeep.Value}
			if d := b.DuplicateDecide.Value; d != nil && p.Duplicates == "near" {
				if _, err := s.usable(ctx, *d, "ai"); err != nil {
					return nil, err
				}
				p.Decide = *d
			}
			if p.HideBelow == nil && p.Duplicates == "off" {
				return nil, huma.Error400BadRequest("nothing to hide: set hide_below or duplicates")
			}
			inst, err := s.scoreInstance(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			if inst != nil {
				for k, d := range s.Registry.Manifests()[inst.Plugin].Enrichments {
					if d.Grid {
						p.ScoreInstance, p.ScoreKey = inst.ID, k
					}
				}
			}
			if p.HideBelow != nil && p.ScoreKey == "" {
				return nil, huma.Error400BadRequest("hide_below needs a score instance whose plugin shows a score on the grid")
			}
			return s.enqueue(ctx, jobs.Spec{Kind: bulk.HideKind, Payload: p})
		})
}
