package api

import (
	"context"
	"errors"
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
		Kind       string        `json:"kind,omitempty" enum:"card,lorebook" doc:"What the query selects; default card."`
		NewCopy    bool          `json:"new_copy,omitempty" doc:"Let a target that can't update cards make another copy"`
		Confirm    string        `json:"confirm,omitempty" doc:"The confirm value from POST /pushes/preview. If the preview would differ now, the push is refused with 409 and the new preview. Without it, a push replaces only app copies unchanged since their last push or resolve."`
	}
}

type bulkPreviewIn struct {
	Body struct {
		InstanceID int64         `json:"instance_id"`
		Query      library.Query `json:"query" doc:"Selects the cards, as in POST /search. Paging is ignored."`
		Kind       string        `json:"kind,omitempty" enum:"card,lorebook" doc:"What the query selects; default card."`
	}
}

type bulkPreviewOut struct{ Body targets.BulkPreview }

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

type autosyncIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Autosync bool `json:"autosync"`
	}
}

type pushesOut struct{ Body []targets.Push }

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
			Description: "Queues a push job. Pushing overwrites changes made in the app; review them first (compare, then resolve) to keep them. " +
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
			Description: "Changes nothing in the app or the library. The app's copy becomes the accepted one, so the binding is ahead where the library differs, and the next push sends the library's. The app's linked world books are left for sync."},
			func(ctx context.Context, in *bindIn) (*bindingOut, error) {
				return s.binding(k.bind(ctx, in.Body.InstanceID, in.ID, in.Body.RemoteID))
			})
	}

	// bulkPush selects what a query push sends and checks that the instance
	// can take it.
	bulkPush := func(ctx context.Context, instanceID int64, q library.Query, kind string) ([]int64, error) {
		if q.Filter == nil && q.Text == "" {
			return nil, huma.Error400BadRequest("bulk actions need a filter or text; to select every card, filter on archived")
		}
		if err := s.usable(ctx, instanceID, "push"); err != nil {
			return nil, err
		}
		search := s.Lib.SearchIDs
		if kind == "lorebook" {
			search = s.Lib.SearchLorebookIDs
			// Fail now; otherwise every child job fails (and retries) on the same reason.
			if err := s.Targets.CheckLorebookPush(ctx, instanceID); err != nil {
				return nil, s.fail(err)
			}
		}
		ids, err := search(ctx, q)
		if err != nil {
			return nil, s.fail(err)
		}
		return ids, nil
	}

	huma.Register(api, huma.Operation{OperationID: "preview-push-cards", Method: http.MethodPost, Path: "/pushes/preview",
		Summary: "Show how the cards a query push would send stand on a target", Tags: tags,
		Description: "Counts the cards by state, as of each binding's last check, and returns the confirm value for POST /pushes."},
		func(ctx context.Context, in *bulkPreviewIn) (*bulkPreviewOut, error) {
			ids, err := bulkPush(ctx, in.Body.InstanceID, in.Body.Query, in.Body.Kind)
			if err != nil {
				return nil, err
			}
			preview := s.Targets.PreviewBulkPush
			if in.Body.Kind == "lorebook" {
				preview = s.Targets.PreviewBulkLorebookPush
			}
			p, err := preview(ctx, in.Body.InstanceID, ids)
			if err != nil {
				return nil, s.fail(err)
			}
			return &bulkPreviewOut{Body: p}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "push-cards", Method: http.MethodPost, Path: "/pushes",
		Summary: "Push many cards or lorebooks to a target",
		Description: "Queues one child job per card or lorebook the query matches. A child fails, leaving the app's copy alone, " +
			"if that copy changed since the preview, or since its last push or resolve when confirm isn't sent.",
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *bulkPushIn) (*jobResp, error) {
			ids, err := bulkPush(ctx, in.Body.InstanceID, in.Body.Query, in.Body.Kind)
			if err != nil {
				return nil, err
			}
			enqueue := s.Targets.EnqueueBulkPush
			if in.Body.Kind == "lorebook" {
				enqueue = s.Targets.EnqueueBulkLorebookPush
			}
			p, err := enqueue(ctx, s.Queue, in.Body.InstanceID, ids, in.Body.NewCopy, in.Body.Confirm)
			var stale *targets.StalePreviewError
			if errors.As(err, &stale) {
				return nil, huma.Error409Conflict(stale.Error(), &huma.ErrorDetail{Location: "body.confirm", Value: stale.Preview})
			}
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

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "update-binding", Method: http.MethodPatch,
		Path: "/bindings/{id}", Summary: "Change a binding's settings", Tags: tags,
		Description: "With autosync on, each sync pushes the library's copy when the card is ahead and the app's copy hasn't changed. " +
			"Destructive, as a push is: autosync replaces the app's copy."},
		func(ctx context.Context, in *autosyncIn) (*bindingOut, error) {
			return s.binding(s.Targets.SetAutosync(ctx, in.ID, in.Body.Autosync))
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
}
