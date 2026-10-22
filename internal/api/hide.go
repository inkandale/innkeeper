package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/libsettings"
	"github.com/inkandale/innkeeper/internal/tier"
)

// libSettings reads library and core settings.
func (s *Server) libSettings() *libsettings.Settings { return libsettings.New(s.Lib, s.Registry) }

// settingOut is option o's value in library lib, the default, and whether lib overrides it.
func settingOut[T comparable](ctx context.Context, s *Server, o libsettings.Option[T], lib int64) (librarySettingOut[T], error) {
	value, def, overridden, err := o.Value(ctx, s.libSettings(), lib)
	return librarySettingOut[T]{Value: value, Default: def, Overridden: overridden}, err
}

// setSetting stores v as option o's core value for a nil lib and as library
// lib's override otherwise; nil clears it.
func setSetting[T comparable](ctx context.Context, s *Server, o libsettings.Option[T], lib *int64, v *T) error {
	text := ""
	if v != nil {
		if err := o.Check(*v); err != nil {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("%s: %v", o.Key, err))
		}
		b, err := json.Marshal(*v)
		if err != nil {
			return err
		}
		text = string(b)
	}
	if lib == nil {
		return s.fail(s.Registry.SetSetting(ctx, o.Key, text))
	}
	return s.fail(s.Lib.SetLibrarySetting(ctx, *lib, o.Key, text))
}

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
		func() error { return setSent(ctx, s, lib, libsettings.HideBelow, in.HideBelow) },
		func() error { return setSent(ctx, s, lib, libsettings.Duplicates, in.Duplicates) },
		func() error { return setSent(ctx, s, lib, libsettings.DuplicateDistance, in.DuplicateDistance) },
		func() error { return setSent(ctx, s, lib, libsettings.DuplicateKeep, in.DuplicateKeep) },
		func() error { return setSent(ctx, s, lib, libsettings.DuplicateDecide, in.DuplicateDecide) },
	} {
		if err := f(); err != nil {
			return err
		}
	}
	return nil
}

func setSent[T comparable](ctx context.Context, s *Server, lib *int64, o libsettings.Option[T], v optional[T]) error {
	if !v.Sent {
		return nil
	}
	return setSetting(ctx, s, o, lib, v.Value)
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
			if err := s.libraryExists(ctx, in.ID); err != nil {
				return nil, err
			}
			p, err := s.libSettings().HidePayload(ctx, in.ID)
			if errors.Is(err, libsettings.ErrNothingToHide) || errors.Is(err, libsettings.ErrNoGridScore) {
				return nil, huma.Error400BadRequest(err.Error())
			}
			if err != nil {
				return nil, s.fail(err)
			}
			return s.enqueue(ctx, jobs.Spec{Kind: bulk.HideKind, Payload: p})
		})
}
