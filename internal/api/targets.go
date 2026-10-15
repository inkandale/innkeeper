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
		Query      library.Query `json:"query" doc:"Selects the cards, as in POST /cards/search. Paging is ignored."`
		Kind       string        `json:"kind,omitempty" enum:"card,lorebook" doc:"What the query selects; default card."`
		NewCopy    bool          `json:"new_copy,omitempty" doc:"Let a target that can't update cards make another copy"`
		Confirm    string        `json:"confirm,omitempty" doc:"The confirm value from POST /pushes/preview. If the preview would differ now, the push is refused with 409 and the new preview. Without it, a push replaces only app copies unchanged since their last push or resolve."`
	}
}

type bulkPreviewIn struct {
	Body struct {
		InstanceID int64         `json:"instance_id"`
		Query      library.Query `json:"query" doc:"Selects the cards, as in POST /cards/search. Paging is ignored."`
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

type bindingEditIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Autosync *bool `json:"autosync,omitempty" doc:"Leave out to keep it as it is"`
		Paused   *bool `json:"paused,omitempty" doc:"Checks skip a paused binding. Only a binding that follows a site source takes it. Leave out to keep it as it is"`
	}
}

type dismissIn struct {
	ID   int64 `path:"id"`
	Body struct {
		AppHash string `json:"app_hash" minLength:"1" doc:"The app_hash of the change the owner saw, from the binding or the changes queue. If the site's data changed since (an avatar change alone does not count), the dismiss is refused with 409 and a fresh comparison."`
	}
}

type pushesOut struct{ Body []targets.Push }

func (s *Server) binding(b targets.Binding, err error) (*bindingOut, error) {
	if err != nil {
		return nil, s.fail(err)
	}
	return &bindingOut{Body: b}, nil
}

// legacyItems are the paths of each kind's app items before they moved under
// /instances/{id}/{plural}.
var legacyItems = map[*library.Kind]string{library.Card: "items", library.Lorebook: "lorebook-items"}

func (s *Server) targetRoutes(api huma.API) {
	tags := []string{"Sync"}
	// Cards and lorebooks have the same push routes, under their own paths.
	for _, k := range library.Kinds {
		ents := s.Lib.Entities(k)
		huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "push-" + k.Name, Method: http.MethodPost, Path: "/" + k.Plural + "/{id}/push",
			Summary: "Push a " + k.Name + " to a target", Tags: tags, DefaultStatus: http.StatusAccepted,
			Description: "Queues a push job. Pushing overwrites changes made in the app; review them first (compare, then resolve) to keep them. " +
				"A card push creates the card's lorebooks the app doesn't have, on a target that keeps lorebooks on their own."},
			func(ctx context.Context, in *pushIn) (*jobResp, error) {
				if _, err := ents.Get(ctx, in.ID); err != nil {
					return nil, s.fail(err)
				}
				if err := s.usable(ctx, in.Body.InstanceID, "push"); err != nil {
					return nil, err
				}
				if in.Body.Confirm != "" {
					p, err := s.Targets.Preview(ctx, k, in.Body.InstanceID, in.ID)
					if err != nil {
						return nil, s.fail(err)
					}
					if p.Confirm != in.Body.Confirm {
						return nil, huma.Error409Conflict("the push would differ from the preview; review the new preview",
							&huma.ErrorDetail{Location: "body.confirm", Value: p})
					}
				}
				j, err := s.Targets.EnqueuePush(ctx, s.Queue, k, in.Body.InstanceID, in.ID, in.Body.NewCopy)
				if err != nil {
					return nil, s.fail(err)
				}
				return &jobResp{Body: toJob(j)}, nil
			})

		huma.Register(api, huma.Operation{OperationID: "preview-push-" + k.Name, Method: http.MethodGet,
			Path: "/" + k.Plural + "/{id}/push-preview", Summary: "Show what a " + k.Name + " push would send and change", Tags: tags,
			Description: "Reads the app's copy and lists the fields the push would change there, the fields it would drop, " +
				"and whether it would make another copy."},
			func(ctx context.Context, in *previewIn) (*previewOut, error) {
				if _, err := ents.Get(ctx, in.ID); err != nil {
					return nil, s.fail(err)
				}
				p, err := s.Targets.Preview(ctx, k, in.InstanceID, in.ID)
				if err != nil {
					return nil, s.fail(err)
				}
				return &previewOut{Body: p}, nil
			})

		huma.Register(api, huma.Operation{OperationID: "list-" + k.Name + "-bindings", Method: http.MethodGet,
			Path: "/" + k.Plural + "/{id}/bindings", Summary: "List a " + k.Name + "'s bindings: the apps it is pushed to and the site sources it follows", Tags: tags},
			func(ctx context.Context, in *idIn) (*bindingsOut, error) {
				if _, err := ents.Get(ctx, in.ID); err != nil {
					return nil, s.fail(err)
				}
				list, err := s.Targets.Bindings(ctx, k, in.ID)
				if err != nil {
					return nil, s.fail(err)
				}
				return &bindingsOut{Body: orEmpty(list)}, nil
			})

		huma.Register(api, huma.Operation{OperationID: "check-" + k.Name + "-sources", Method: http.MethodPost,
			Path: "/" + k.Plural + "/{id}/source-check", Summary: "Check a " + k.Name + "'s site sources for updates now", Tags: tags,
			Description: "Follows the " + k.Name + "'s sources and reads them back, as the scheduled check does. Returns its bindings, " +
				"or the first failure's error if a source couldn't be followed or checked."},
			func(ctx context.Context, in *idIn) (*bindingsOut, error) {
				if _, err := ents.Get(ctx, in.ID); err != nil {
					return nil, s.fail(err)
				}
				list, err := s.Targets.CheckEntitySources(ctx, k, in.ID)
				if err != nil {
					return nil, s.fail(err)
				}
				return &bindingsOut{Body: orEmpty(list)}, nil
			})

		registerMoved(api, huma.Operation{OperationID: "list-" + k.Name + "-items", Method: http.MethodGet,
			Path: "/instances/{id}/" + k.Plural, Summary: "List the " + k.Plural + " a target app has", Tags: tags,
			Description: "Reads the app. Each item has a binding_id if a binding already uses it."}, "/instances/{id}/"+legacyItems[k],
			func(ctx context.Context, in *idIn) (*itemsOut, error) {
				list, err := s.Targets.Items(ctx, k, in.ID)
				if err != nil {
					return nil, s.fail(err)
				}
				return &itemsOut{Body: orEmpty(list)}, nil
			})

		huma.Register(api, huma.Operation{OperationID: "bind-" + k.Name, Method: http.MethodPost,
			Path: "/" + k.Plural + "/{id}/bind", Summary: "Bind a " + k.Name + " to one the app already has", Tags: tags,
			Description: "Changes nothing in the app or the library. The app's copy becomes the accepted one, so the binding is ahead where the library differs, and the next push sends the library's. The app's linked world books are left for sync."},
			func(ctx context.Context, in *bindIn) (*bindingOut, error) {
				return s.binding(s.Targets.Bind(ctx, k, in.Body.InstanceID, in.ID, in.Body.RemoteID))
			})
	}

	// bulkPush selects what a query push sends and checks that the instance
	// can take it.
	bulkPush := func(ctx context.Context, instanceID int64, q library.Query, k *library.Kind) ([]int64, error) {
		if q.Filter == nil && q.Text == "" {
			return nil, huma.Error400BadRequest("bulk actions need a filter or text; to select every card, filter on archived")
		}
		if err := s.usable(ctx, instanceID, "push"); err != nil {
			return nil, err
		}
		if k == library.Lorebook {
			// Fail now; otherwise every child job fails (and retries) on the same reason.
			if err := s.Targets.CheckLorebookPush(ctx, instanceID); err != nil {
				return nil, s.fail(err)
			}
		}
		ids, err := s.Lib.SearchIDs(ctx, k, q)
		if err != nil {
			return nil, s.fail(err)
		}
		return ids, nil
	}

	huma.Register(api, huma.Operation{OperationID: "preview-push-cards", Method: http.MethodPost, Path: "/pushes/preview",
		Summary: "Show how the cards a query push would send stand on a target", Tags: tags,
		Description: "Counts the cards by state, as of each binding's last check, and returns the confirm value for POST /pushes."},
		func(ctx context.Context, in *bulkPreviewIn) (*bulkPreviewOut, error) {
			k := kindNamed(in.Body.Kind)
			ids, err := bulkPush(ctx, in.Body.InstanceID, in.Body.Query, k)
			if err != nil {
				return nil, err
			}
			p, err := s.Targets.PreviewBulkPush(ctx, k, in.Body.InstanceID, ids)
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
			k := kindNamed(in.Body.Kind)
			ids, err := bulkPush(ctx, in.Body.InstanceID, in.Body.Query, k)
			if err != nil {
				return nil, err
			}
			p, err := s.Targets.EnqueueBulkPush(ctx, s.Queue, k, in.Body.InstanceID, ids, in.Body.NewCopy, in.Body.Confirm)
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
		Description: "With autosync on, each sync pushes the library's copy when the card is ahead and the app's copy hasn't changed; " +
			"for a binding that follows a site source, each source check takes an update that merges without conflicts. " +
			"A paused binding isn't checked. Destructive, as a push is: autosync replaces the app's copy."},
		func(ctx context.Context, in *bindingEditIn) (*bindingOut, error) {
			// Paused first, since only a followed binding takes it.
			if in.Body.Paused != nil {
				if _, err := s.Targets.SetPaused(ctx, in.ID, *in.Body.Paused); err != nil {
					return nil, s.fail(err)
				}
			}
			if in.Body.Autosync != nil {
				if _, err := s.Targets.SetAutosync(ctx, in.ID, *in.Body.Autosync); err != nil {
					return nil, s.fail(err)
				}
			}
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

	huma.Register(api, huma.Operation{OperationID: "dismiss-binding-change", Method: http.MethodPost, Path: "/bindings/{id}/dismiss",
		Summary: "Ignore a site source's change", Tags: tags,
		Description: "Accepts the site's copy without changing the library; the next change on the site shows again. " +
			"Only for a binding that follows a site source."},
		func(ctx context.Context, in *dismissIn) (*bindingOut, error) {
			return s.binding(s.Targets.Dismiss(ctx, in.ID, in.Body.AppHash))
		})

	huma.Register(api, huma.Operation{OperationID: "start-source-check", Method: http.MethodPost,
		Path: "/source-checks", Summary: "Check every site source for updates", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Follows each card's and lorebook's site sources and reads them back. With autosync on for a binding, " +
			"takes an update that merges without conflicts. Returns the check already queued or running, if there is one."},
		func(ctx context.Context, _ *struct{}) (*jobResp, error) {
			j, err := s.Queue.EnqueueOnce(ctx, jobs.Spec{Kind: targets.SourceCheckKind})
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "start-scan", Method: http.MethodPost,
		Path: "/scans", Summary: "Refresh: check every app for changes", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Reads every bound item back from its app and counts the app items nothing is bound to. " +
			"Changes nothing in the library or the apps. Returns the scan already queued or running, if there is one."},
		func(ctx context.Context, _ *struct{}) (*jobResp, error) {
			j, err := s.Queue.EnqueueOnce(ctx, jobs.Spec{Kind: targets.ScanKind})
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})
}
