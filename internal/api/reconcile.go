package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/targets"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

type reconcileIn struct{ Body targets.ReconcileParams }

type compareCardIn struct {
	ID       int64  `path:"id"`
	RemoteID string `path:"remote_id" doc:"The app's id for the item, path-escaped"`
	CardID   int64  `query:"card_id" doc:"The library card to compare with; 0 means the bound one"`
}

type compareLorebookIn struct {
	ID         int64  `path:"id"`
	RemoteID   string `path:"remote_id" doc:"The app's id for the world book, path-escaped"`
	LorebookID int64  `query:"lorebook_id" doc:"The library lorebook to compare with; 0 means the bound one"`
}

type comparisonOut struct{ Body targets.Comparison }

type resolveIn struct {
	ID       int64  `path:"id"`
	RemoteID string `path:"remote_id" doc:"The app's id for the item, path-escaped"`
	Body     targets.Resolution
}

type resolveOut struct{ Body targets.ResolveResult }

type resolutionsIn struct {
	Body struct {
		Resolutions []targets.Resolution `json:"resolutions" minItems:"1"`
	}
}

func (s *Server) reconcileRoutes(api huma.API) {
	tags := []string{"Sync"}

	// Read tier: it changes nothing unless the instance has auto_bind_exact,
	// which only a destructive token can turn on.
	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "start-reconcile", Method: http.MethodPost,
		Path: "/reconciles", Summary: "Line up a target app against the library", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Queues a reconcile job. Read the snapshot from GET /jobs/{id}: its progress is the snapshot once the job finishes."},
		func(ctx context.Context, in *reconcileIn) (*jobResp, error) {
			if err := s.usable(ctx, in.Body.InstanceID, "pull"); err != nil {
				return nil, err
			}
			j, err := s.Targets.EnqueueReconcile(ctx, s.Queue, in.Body)
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "compare-item", Method: http.MethodGet,
		Path: "/instances/{id}/items/{remote_id}/compare", Summary: "Compare an app character with a library card", Tags: tags,
		Description: "Reads the app. Resolve with the comparison's app_hash, head_revision_id, and accepted_hash."},
		func(ctx context.Context, in *compareCardIn) (*comparisonOut, error) {
			c, err := s.Targets.CompareItem(ctx, in.ID, in.RemoteID, in.CardID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &comparisonOut{Body: c}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "compare-lorebook-item", Method: http.MethodGet,
		Path: "/instances/{id}/lorebook-items/{remote_id}/compare", Summary: "Compare an app world book with a library lorebook", Tags: tags,
		Description: "Reads the app. Resolve with the comparison's app_hash, head_revision_id, and accepted_hash."},
		func(ctx context.Context, in *compareLorebookIn) (*comparisonOut, error) {
			c, err := s.Targets.CompareLorebookItem(ctx, in.ID, in.RemoteID, in.LorebookID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &comparisonOut{Body: c}, nil
		})

	for _, k := range []struct {
		name, itemsPath string
		resolve         func(context.Context, targets.Resolution) (targets.ResolveResult, error)
	}{
		{"item", "items", s.Targets.Resolve},
		{"lorebook-item", "lorebook-items", s.Targets.ResolveLorebook},
	} {
		huma.Register(api, huma.Operation{OperationID: "resolve-" + k.name, Method: http.MethodPost,
			Path: "/instances/{id}/" + k.itemsPath + "/{remote_id}/resolve", Summary: "Bind, import, or skip an app " + k.name, Tags: tags,
			Description: "The path sets instance_id and remote_id. Changes the library and the binding, never the app; push afterwards. " +
				"If the app's copy, the library, or the binding changed since the comparison, returns 409 with a fresh comparison."},
			func(ctx context.Context, in *resolveIn) (*resolveOut, error) {
				r := in.Body
				r.InstanceID, r.RemoteID = in.ID, in.RemoteID
				res, err := k.resolve(ctx, r)
				if err != nil {
					return nil, s.fail(err)
				}
				return &resolveOut{Body: res}, nil
			})
	}

	huma.Register(api, huma.Operation{OperationID: "resolve-items", Method: http.MethodPost, Path: "/resolutions",
		Summary: "Resolve many app items", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Queues one child job per resolution; one that fails doesn't stop the others. Each child's progress has its outcome."},
		func(ctx context.Context, in *resolutionsIn) (*jobResp, error) {
			p, err := s.Targets.EnqueueResolve(ctx, s.Queue, in.Body.Resolutions)
			if err != nil {
				return nil, s.fail(err)
			}
			j, err := s.job(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			return &jobResp{Body: j}, nil
		})
}
