package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/maintenance"
	"github.com/inkandale/innkeeper/internal/tier"
)

type maintenancePreviewIn struct {
	Keep int `query:"keep" default:"3" minimum:"1" maximum:"100" doc:"Values to keep per instance, card and key."`
	Days int `query:"days" default:"30" minimum:"1" doc:"Purge cards archived at least this many days ago."`
}

// MaintenancePreview renames maintenance.Preview, whose schema name would collide with wire.Preview.
type MaintenancePreview maintenance.Preview

type maintenancePreviewOut struct{ Body MaintenancePreview }

type maintenanceRunIn struct {
	Body struct {
		Action string `json:"action" enum:"trim-history,stale-vectors,clear-failures,vacuum,purge"`
		Keep   int    `json:"keep,omitempty" minimum:"1" maximum:"100" default:"3"`
		Days   int    `json:"days,omitempty" minimum:"1" default:"30"`
	}
}

func (s *Server) maintenanceRoutes(api huma.API) {
	tags := []string{"Maintenance"}
	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "preview-maintenance", Method: http.MethodGet,
		Path: "/maintenance", Summary: "Preview the database cleanups", Tags: tags},
		func(ctx context.Context, in *maintenancePreviewIn) (*maintenancePreviewOut, error) {
			p, err := s.Maintenance.Preview(ctx, in.Keep, in.Days)
			if err != nil {
				return nil, s.fail(err)
			}
			return &maintenancePreviewOut{Body: MaintenancePreview(p)}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "run-maintenance", Method: http.MethodPost,
		Path: "/maintenance/runs", Summary: "Run a database cleanup", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Queues the cleanup. While one is queued or running, returns it instead. Trim, stale vectors, and purge can't be undone."},
		func(ctx context.Context, in *maintenanceRunIn) (*jobResp, error) {
			b := in.Body
			// Backstop for the tags: days < 1 would purge every archived card.
			if b.Keep < 1 || b.Days < 1 {
				return nil, huma.Error422UnprocessableEntity("keep and days must be at least 1")
			}
			j, err := s.Queue.EnqueueOnce(ctx, jobs.Spec{Kind: maintenance.Kind, Payload: maintenance.Payload{Action: b.Action, Keep: b.Keep, Days: b.Days}})
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})
}
