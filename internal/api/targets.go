package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/targets"
)

type pushIn struct {
	ID   int64 `path:"id"`
	Body struct {
		InstanceID int64 `json:"instance_id"`
		NewCopy    bool  `json:"new_copy,omitempty" doc:"Let a target that can't update cards make another copy"`
	}
}

type bulkPushIn struct {
	Body struct {
		InstanceID int64         `json:"instance_id"`
		Query      library.Query `json:"query" doc:"Selects the cards, as in POST /search. Paging is ignored."`
		NewCopy    bool          `json:"new_copy,omitempty" doc:"Let a target that can't update cards make another copy"`
	}
}

type previewIn struct {
	ID         int64 `path:"id"`
	InstanceID int64 `query:"instance_id" required:"true"`
}

type previewOut struct{ Body targets.Preview }

type bindingOut struct{ Body targets.Binding }

type bindingsOut struct{ Body []targets.Binding }

type pushesOut struct{ Body []targets.Push }

type adoptIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Into           string `json:"into" enum:"base,override,ignore" doc:"base: into the card's own data. override: into this target's override. ignore: keep the library as it is and stop reporting these changes."`
		BaseRevisionID int64  `json:"base_revision_id,omitempty" doc:"The card's head revision. Required unless into is ignore; a stale one returns 409."`
	}
}

func (s *Server) binding(b targets.Binding, err error) (*bindingOut, error) {
	if err != nil {
		return nil, s.fail(err)
	}
	return &bindingOut{Body: b}, nil
}

func (s *Server) targetRoutes(api huma.API) {
	tags := []string{"Sync"}
	huma.Register(api, huma.Operation{OperationID: "push-card", Method: http.MethodPost, Path: "/cards/{id}/push",
		Summary: "Push a card to a target", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Queues a push job. Pushing overwrites changes made in the app; adopt them first to keep them."},
		func(ctx context.Context, in *pushIn) (*jobResp, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			if err := s.usable(ctx, in.Body.InstanceID, "push"); err != nil {
				return nil, err
			}
			j, err := s.Targets.EnqueuePush(ctx, s.Queue, in.Body.InstanceID, in.ID, in.Body.NewCopy)
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "push-cards", Method: http.MethodPost, Path: "/pushes",
		Summary: "Push many cards to a target", Description: "Queues one child job per card the query matches.",
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *bulkPushIn) (*jobResp, error) {
			q := in.Body.Query
			if q.Filter == nil && q.Text == "" {
				return nil, huma.Error400BadRequest("bulk actions need a filter or text; to select every card, filter on archived")
			}
			if err := s.usable(ctx, in.Body.InstanceID, "push"); err != nil {
				return nil, err
			}
			ids, err := s.Lib.SearchIDs(ctx, q)
			if err != nil {
				return nil, s.fail(err)
			}
			p, err := s.Targets.EnqueueBulkPush(ctx, s.Queue, in.Body.InstanceID, ids, in.Body.NewCopy)
			if err != nil {
				return nil, s.fail(err)
			}
			j, err := s.job(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			return &jobResp{Body: j}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "preview-push", Method: http.MethodGet, Path: "/cards/{id}/push-preview",
		Summary: "Show what a push would send", Tags: tags,
		Description: "Asks the target plugin what it stores. Lists fields the push would drop, and whether it would make another copy."},
		func(ctx context.Context, in *previewIn) (*previewOut, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			p, err := s.Targets.Preview(ctx, in.InstanceID, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &previewOut{Body: p}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-card-bindings", Method: http.MethodGet, Path: "/cards/{id}/bindings",
		Summary: "List the targets a card is pushed to", Tags: tags},
		func(ctx context.Context, in *idIn) (*bindingsOut, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			list, err := s.Targets.Bindings(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &bindingsOut{Body: orEmpty(list)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-binding", Method: http.MethodGet, Path: "/bindings/{id}",
		Summary: "Get a binding", Tags: tags},
		func(ctx context.Context, in *idIn) (*bindingOut, error) {
			return s.binding(s.Targets.Binding(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{OperationID: "list-pushes", Method: http.MethodGet, Path: "/bindings/{id}/pushes",
		Summary: "List a binding's pushes", Description: "Newest first.", Tags: tags},
		func(ctx context.Context, in *idIn) (*pushesOut, error) {
			list, err := s.Targets.Pushes(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &pushesOut{Body: orEmpty(list)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "unbind", Method: http.MethodDelete, Path: "/bindings/{id}",
		Summary: "Unbind a card from a target", Description: "Forgets the binding and its pushes. The app keeps its copy.",
		Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			return nil, s.fail(s.Targets.Unbind(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{OperationID: "check-binding", Method: http.MethodPost, Path: "/bindings/{id}/check",
		Summary: "Check the app's copy for changes", Description: "Reads the card back from the app now.", Tags: tags},
		func(ctx context.Context, in *idIn) (*bindingOut, error) {
			return s.binding(s.Targets.Check(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{OperationID: "adopt", Method: http.MethodPost, Path: "/bindings/{id}/adopt",
		Summary: "Adopt the app's changes", Tags: tags,
		Description: "Changes to character_book aren't adopted. To discard the app's changes, push instead."},
		func(ctx context.Context, in *adoptIn) (*bindingOut, error) {
			if in.Body.Into != "ignore" && in.Body.BaseRevisionID == 0 {
				return nil, huma.Error400BadRequest("base_revision_id is required unless into is ignore")
			}
			return s.binding(s.Targets.Adopt(ctx, in.ID, in.Body.Into, in.Body.BaseRevisionID))
		})
}
