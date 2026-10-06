package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/targets"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

type pushIn struct {
	ID   int64 `path:"id"`
	Body struct {
		InstanceID int64  `json:"instance_id"`
		NewCopy    bool   `json:"new_copy,omitempty" doc:"Let a target that can't update cards make another copy"`
		Confirm    string `json:"confirm,omitempty" doc:"The confirm value from the push preview. If the preview would differ now, the push is refused with 409 and the new preview."`
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

type itemsOut struct{ Body []targets.Item }

type bindIn struct {
	ID   int64 `path:"id"`
	Body struct {
		InstanceID int64  `json:"instance_id"`
		RemoteID   string `json:"remote_id" minLength:"1" doc:"The app's id for the item, from its item list"`
	}
}

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
	// Cards and lorebooks have the same push routes, under their own paths.
	type syncedKind struct {
		name, plural, itemsPath string
		get                     func(context.Context, int64) (library.Entity, error)
		enqueue                 func(context.Context, *jobs.Queue, int64, int64, bool) (jobs.Job, error)
		preview                 func(context.Context, int64, int64) (targets.Preview, error)
		bindings                func(context.Context, int64) ([]targets.Binding, error)
		items                   func(context.Context, int64) ([]targets.Item, error)
		bind                    func(context.Context, int64, int64, string) (targets.Binding, error)
	}
	for _, k := range []syncedKind{
		{"card", "cards", "items", s.Lib.Cards.Get, s.Targets.EnqueuePush, s.Targets.Preview, s.Targets.Bindings,
			s.Targets.Items, s.Targets.Bind},
		{"lorebook", "lorebooks", "lorebook-items", s.Lib.Lorebooks.Get, s.Targets.EnqueueLorebookPush, s.Targets.PreviewLorebook,
			s.Targets.LorebookBindings, s.Targets.LorebookItems, s.Targets.BindLorebook},
	} {
		huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "push-" + k.name, Method: http.MethodPost, Path: "/" + k.plural + "/{id}/push",
			Summary: "Push a " + k.name + " to a target", Tags: tags, DefaultStatus: http.StatusAccepted,
			Description: "Queues a push job. Pushing overwrites changes made in the app; adopt them first to keep them. " +
				"A card push creates the card's lorebooks the app doesn't have, on a target that keeps lorebooks on their own."},
			func(ctx context.Context, in *pushIn) (*jobResp, error) {
				if _, err := k.get(ctx, in.ID); err != nil {
					return nil, s.fail(err)
				}
				if err := s.usable(ctx, in.Body.InstanceID, "push"); err != nil {
					return nil, err
				}
				if in.Body.Confirm != "" {
					p, err := k.preview(ctx, in.Body.InstanceID, in.ID)
					if err != nil {
						return nil, s.fail(err)
					}
					if p.Confirm != in.Body.Confirm {
						return nil, huma.Error409Conflict("the push would differ from the preview; review the new preview",
							&huma.ErrorDetail{Location: "body.confirm", Value: p})
					}
				}
				j, err := k.enqueue(ctx, s.Queue, in.Body.InstanceID, in.ID, in.Body.NewCopy)
				if err != nil {
					return nil, s.fail(err)
				}
				return &jobResp{Body: toJob(j)}, nil
			})

		huma.Register(api, huma.Operation{OperationID: "preview-push-" + k.name, Method: http.MethodGet,
			Path: "/" + k.plural + "/{id}/push-preview", Summary: "Show what a " + k.name + " push would send and change", Tags: tags,
			Description: "Reads the app's copy and lists the fields the push would change there, the fields it would drop, " +
				"and whether it would make another copy."},
			func(ctx context.Context, in *previewIn) (*previewOut, error) {
				if _, err := k.get(ctx, in.ID); err != nil {
					return nil, s.fail(err)
				}
				p, err := k.preview(ctx, in.InstanceID, in.ID)
				if err != nil {
					return nil, s.fail(err)
				}
				return &previewOut{Body: p}, nil
			})

		huma.Register(api, huma.Operation{OperationID: "list-" + k.name + "-bindings", Method: http.MethodGet,
			Path: "/" + k.plural + "/{id}/bindings", Summary: "List the targets a " + k.name + " is pushed to", Tags: tags},
			func(ctx context.Context, in *idIn) (*bindingsOut, error) {
				if _, err := k.get(ctx, in.ID); err != nil {
					return nil, s.fail(err)
				}
				list, err := k.bindings(ctx, in.ID)
				if err != nil {
					return nil, s.fail(err)
				}
				return &bindingsOut{Body: orEmpty(list)}, nil
			})

		huma.Register(api, huma.Operation{OperationID: "list-" + k.name + "-items", Method: http.MethodGet,
			Path: "/instances/{id}/" + k.itemsPath, Summary: "List the " + k.plural + " a target app has", Tags: tags,
			Description: "Reads the app. Each item has a binding_id if a binding already uses it."},
			func(ctx context.Context, in *idIn) (*itemsOut, error) {
				list, err := k.items(ctx, in.ID)
				if err != nil {
					return nil, s.fail(err)
				}
				return &itemsOut{Body: orEmpty(list)}, nil
			})

		huma.Register(api, huma.Operation{OperationID: "bind-" + k.name, Method: http.MethodPost,
			Path: "/" + k.plural + "/{id}/bind", Summary: "Bind a " + k.name + " to one the app already has", Tags: tags,
			Description: "Changes nothing in the app or the library. The app's copy becomes the accepted one, so the binding is ahead where the library differs, and the next push sends the library's."},
			func(ctx context.Context, in *bindIn) (*bindingOut, error) {
				return s.binding(k.bind(ctx, in.Body.InstanceID, in.ID, in.Body.RemoteID))
			})
	}

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "push-cards", Method: http.MethodPost, Path: "/pushes",
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

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "unbind", Method: http.MethodDelete, Path: "/bindings/{id}",
		Summary: "Unbind a card or lorebook from a target", Description: "Forgets the binding and its pushes. The app keeps its copy.",
		Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			return nil, s.fail(s.Targets.Unbind(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "check-binding", Method: http.MethodPost, Path: "/bindings/{id}/check",
		Summary: "Check the app's copy for changes", Description: "Reads the card back from the app now.", Tags: tags},
		func(ctx context.Context, in *idIn) (*bindingOut, error) {
			return s.binding(s.Targets.Check(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "adopt", Method: http.MethodPost, Path: "/bindings/{id}/adopt",
		Summary: "Adopt the app's changes", Tags: tags,
		Description: "Adopts into the card or lorebook the binding is for. A card's changes to character_book aren't adopted. " +
			"To discard the app's changes, push instead."},
		func(ctx context.Context, in *adoptIn) (*bindingOut, error) {
			if in.Body.Into != "ignore" && in.Body.BaseRevisionID == 0 {
				return nil, huma.Error400BadRequest("base_revision_id is required unless into is ignore")
			}
			return s.binding(s.Targets.Adopt(ctx, in.ID, in.Body.Into, in.Body.BaseRevisionID))
		})
}
