package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/libsettings"
	"github.com/inkandale/innkeeper/internal/tier"
)

// librarySettingOut is one library setting.
type librarySettingOut[T any] struct {
	Value      *T   `json:"value" doc:"What the library uses: its override, or the default."`
	Default    *T   `json:"default" doc:"The core's value, which a library without an override follows."`
	Overridden bool `json:"overridden"`
}

type librarySettings struct {
	ScoreInstance     librarySettingOut[int64]   `json:"score_instance" doc:"The enrich instance whose values this library's grid shows and hide_below reads, and that enrichment filters and sort naming no instance use."`
	HideBelow         librarySettingOut[float64] `json:"hide_below" doc:"Hiding archives cards whose grid score is below this; null hides none for their score."`
	Duplicates        librarySettingOut[string]  `json:"duplicates" doc:"Hiding archives duplicates: off, exact (the same content), or near (text, or the avatar of a card with the same name, within duplicate_distance bits). When not off, an import that changes cards runs hiding."`
	DuplicateDistance librarySettingOut[int]     `json:"duplicate_distance" doc:"For near duplicates, the most bits two cards' text or avatar hashes may differ by."`
	DuplicateKeep     librarySettingOut[string]  `json:"duplicate_keep" doc:"The duplicate hiding keeps: the best score, the oldest, or the newest."`
	DuplicateDecide   librarySettingOut[int64]   `json:"duplicate_decide" doc:"For near duplicates, the ai instance that confirms each pair is the same card, or null for distance alone."`
}

type librarySettingsOut struct{ Body librarySettings }

type librarySettingsIn struct {
	ID int64 `path:"id"`
}

type patchLibrarySettingsIn struct {
	ID   int64 `path:"id"`
	Body struct {
		ScoreInstance optional[int64] `json:"score_instance,omitempty" doc:"An enrich instance's ID, or null to follow the default."`
		// Null follows the default.
		HideOptionsIn
	}
}

// libraryExists is nil if library id exists, and a 404 if it doesn't.
func (s *Server) libraryExists(ctx context.Context, id int64) error {
	ok, err := s.Lib.HasLibrary(ctx, id)
	if err != nil {
		return s.fail(err)
	}
	if !ok {
		return huma.Error404NotFound(fmt.Sprintf("library %d doesn't exist", id))
	}
	return nil
}

func (s *Server) librarySettings(ctx context.Context, id int64) (*librarySettingsOut, error) {
	if err := s.libraryExists(ctx, id); err != nil {
		return nil, err
	}
	set := s.libSettings()
	insts, err := set.Enrichers(ctx)
	if err != nil {
		return nil, s.fail(err)
	}
	def, err := s.Registry.Setting(ctx, libsettings.ScoreInstanceKey)
	if err != nil {
		return nil, s.fail(err)
	}
	v, overridden, err := set.Get(ctx, id, libsettings.ScoreInstanceKey)
	if err != nil {
		return nil, s.fail(err)
	}
	out := &librarySettingsOut{}
	out.Body.ScoreInstance.Overridden = overridden
	if i := libsettings.ScoreNamed(v, insts); i != nil {
		out.Body.ScoreInstance.Value = &i.ID
	}
	if i := libsettings.ScoreNamed(def, insts); i != nil {
		out.Body.ScoreInstance.Default = &i.ID
	}
	b := &out.Body
	if b.HideBelow, err = settingOut(ctx, s, libsettings.HideBelow, id); err != nil {
		return nil, s.fail(err)
	}
	if b.Duplicates, err = settingOut(ctx, s, libsettings.Duplicates, id); err != nil {
		return nil, s.fail(err)
	}
	if b.DuplicateDistance, err = settingOut(ctx, s, libsettings.DuplicateDistance, id); err != nil {
		return nil, s.fail(err)
	}
	if b.DuplicateKeep, err = settingOut(ctx, s, libsettings.DuplicateKeep, id); err != nil {
		return nil, s.fail(err)
	}
	if b.DuplicateDecide, err = settingOut(ctx, s, libsettings.DuplicateDecide, id); err != nil {
		return nil, s.fail(err)
	}
	return out, nil
}

func (s *Server) librarySettingsRoutes(api huma.API) {
	tags := []string{"Libraries"}
	huma.Register(api, huma.Operation{OperationID: "get-library-settings", Method: http.MethodGet, Path: "/libraries/{id}/settings",
		Summary: "Get a library's settings", Tags: tags,
		Description: "Each setting's value in this library, the core's default, and whether the library overrides it."},
		func(ctx context.Context, in *librarySettingsIn) (*librarySettingsOut, error) {
			return s.librarySettings(ctx, in.ID)
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "update-library-settings", Method: http.MethodPatch,
		Path: "/libraries/{id}/settings", Summary: "Change a library's settings", Tags: tags,
		Description: "Overrides a setting for this library; null follows the default again. Settings left out don't change."},
		func(ctx context.Context, in *patchLibrarySettingsIn) (*librarySettingsOut, error) {
			if _, err := s.librarySettings(ctx, in.ID); err != nil {
				return nil, err
			}
			b := in.Body
			if b.ScoreInstance.Sent {
				if err := s.setScoreInstance(ctx, &in.ID, b.ScoreInstance.Value); err != nil {
					return nil, err
				}
			}
			if err := b.HideOptionsIn.set(ctx, s, &in.ID); err != nil {
				return nil, err
			}
			return s.librarySettings(ctx, in.ID)
		})
}
