package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/targets"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

type reconcileIn struct{ Body targets.ReconcileParams }

type syncIn struct{ Body targets.SyncParams }

type compareCardIn struct {
	ID       int64  `path:"id"`
	RemoteID string `path:"remote_id" doc:"The app's id for the item, path-escaped"`
	CardID   int64  `query:"card_id" doc:"The library card to compare with; 0 means the bound one"`
	Base     string `query:"base" enum:"none" required:"false" doc:"none leaves out the binding's accepted copy, so each difference is a choice; resolve with base none"`
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

type changesIn struct {
	InstanceID int64  `query:"instance_id" doc:"Only this app's changes; counts still cover every app"`
	State      string `query:"state" enum:"differs,both_changed,ahead,missing,error,new,update" doc:"Only this state"`
}

type changesOut struct{ Body targets.Changes }

func (s *Server) reconcileRoutes(api huma.API) {
	tags := []string{"Sync"}

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "start-sync", Method: http.MethodPost,
		Path: "/syncs", Summary: "Sync every target app with the library", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Queues a sync of every app, one at a time, or returns the one already queued or running. " +
			"Each app's snapshot is a finished reconcile job named in the result's apps; read it from GET /jobs/{id}."},
		func(ctx context.Context, in *syncIn) (*jobResp, error) {
			if in.Body.InstanceID != 0 {
				if err := s.usable(ctx, in.Body.InstanceID, "pull"); err != nil {
					return nil, err
				}
			}
			j, err := s.Targets.EnqueueSync(ctx, s.Queue, in.Body)
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "start-reconcile", Method: http.MethodPost,
		Path: "/reconciles", Summary: "Sync a target app with the library", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Queues a sync. With apply obvious (the default) it binds exact matches and imports new items; apply none previews. Read the snapshot from GET /jobs/{id} once the job finishes."},
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

	huma.Register(api, huma.Operation{OperationID: "list-changes", Method: http.MethodGet, Path: "/sync/changes",
		Summary: "List the cards that need the owner", Tags: tags,
		Description: "One row per card with a change in some app, and app items no card matches first. Built from each app's latest sync and the binding statuses."},
		func(ctx context.Context, in *changesIn) (*changesOut, error) {
			ch, err := s.Targets.Changes(ctx, targets.ChangesFilter{InstanceID: in.InstanceID, State: in.State})
			if err != nil {
				return nil, s.fail(err)
			}
			return &changesOut{Body: ch}, nil
		})

	registerMoved(api, huma.Operation{OperationID: "compare-card-item", Method: http.MethodGet,
		Path: "/instances/{id}/cards/{remote_id}/compare", Summary: "Compare an app character with a library card", Tags: tags,
		Description: "Reads the app. Resolve with the comparison's app_hash, head_revision_id, and accepted_hash."},
		"/instances/{id}/items/{remote_id}/compare",
		func(ctx context.Context, in *compareCardIn) (*comparisonOut, error) {
			compare := s.Targets.CompareItem
			if in.Base == "none" {
				compare = s.Targets.CompareItemTwoWay
			}
			c, err := compare(ctx, library.Card, in.ID, in.RemoteID, in.CardID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &comparisonOut{Body: c}, nil
		})

	registerMoved(api, huma.Operation{OperationID: "compare-lorebook-item", Method: http.MethodGet,
		Path: "/instances/{id}/lorebooks/{remote_id}/compare", Summary: "Compare an app world book with a library lorebook", Tags: tags,
		Description: "Reads the app. Resolve with the comparison's app_hash, head_revision_id, and accepted_hash."},
		"/instances/{id}/lorebook-items/{remote_id}/compare",
		func(ctx context.Context, in *compareLorebookIn) (*comparisonOut, error) {
			c, err := s.Targets.CompareItem(ctx, library.Lorebook, in.ID, in.RemoteID, in.LorebookID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &comparisonOut{Body: c}, nil
		})

	for _, k := range library.Kinds {
		registerMoved(api, huma.Operation{OperationID: "resolve-" + k.Name + "-item", Method: http.MethodPost,
			Path: "/instances/{id}/" + k.Plural + "/{remote_id}/resolve", Summary: "Bind, import, or skip an app " + k.Name, Tags: tags,
			Description: "The path sets instance_id and remote_id. Changes the library and the binding, never the app; push afterwards. " +
				"If the app's copy, the library, or the binding changed since the comparison, returns 409 with a fresh comparison."},
			"/instances/{id}/"+legacyItems[k]+"/{remote_id}/resolve",
			func(ctx context.Context, in *resolveIn) (*resolveOut, error) {
				r := in.Body
				r.InstanceID, r.RemoteID = in.ID, in.RemoteID
				res, err := s.Targets.Resolve(ctx, k, r)
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
