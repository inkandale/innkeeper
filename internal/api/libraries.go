package api

import (
	"cmp"
	"context"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/tier"
)

type librariesOut struct{ Body []library.LibrarySummary }

type libraryResp struct{ Body library.LibrarySummary }

type libraryNameIn struct {
	Body struct {
		Name string `json:"name" minLength:"1"`
	}
}

type renameLibraryIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Name string `json:"name" minLength:"1"`
	}
}

type deleteLibraryIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Confirm string `json:"confirm" doc:"The library's exact name"`
	}
}

// libraryQuery is a library named in a query parameter.
type libraryQuery struct {
	Library int64 `query:"library" doc:"The library; default 1"`
}

// checkLibrary returns the library id names, DefaultLibrary for 0, or a 422 if it
// doesn't exist.
func (s *Server) checkLibrary(ctx context.Context, id int64) (int64, error) {
	id = cmp.Or(id, library.DefaultLibrary)
	ok, err := s.Lib.HasLibrary(ctx, id)
	if err != nil {
		return 0, s.fail(err)
	}
	if !ok {
		return 0, huma.Error422UnprocessableEntity(fmt.Sprintf("library %d doesn't exist", id))
	}
	return id, nil
}

func (s *Server) libraryRoutes(api huma.API) {
	tags := []string{"Libraries"}
	huma.Register(api, huma.Operation{OperationID: "list-libraries", Method: http.MethodGet, Path: "/libraries",
		Summary: "List libraries", Description: "Oldest first, with how many cards and lorebooks each holds, archived ones included.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*librariesOut, error) {
			l, err := s.Lib.Libraries(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &librariesOut{Body: l}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "create-library", Method: http.MethodPost, Path: "/libraries",
		Summary: "Create a library", Description: "A taken name returns 409.", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *libraryNameIn) (*libraryResp, error) {
			l, err := s.Lib.CreateLibrary(ctx, in.Body.Name)
			if err != nil {
				return nil, s.fail(err)
			}
			return &libraryResp{Body: l}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "rename-library", Method: http.MethodPatch, Path: "/libraries/{id}",
		Summary: "Rename a library", Description: "A taken name returns 409.", Tags: tags},
		func(ctx context.Context, in *renameLibraryIn) (*libraryResp, error) {
			l, err := s.Lib.RenameLibrary(ctx, in.ID, in.Body.Name)
			if err != nil {
				return nil, s.fail(err)
			}
			return &libraryResp{Body: l}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "delete-library", Method: http.MethodDelete, Path: "/libraries/{id}",
		Summary: "Delete a library with its cards and lorebooks",
		Description: "Queues a job that deletes the library's cards and lorebooks, with their history, bindings, and sources, " +
			"then its collections and the library. This can't be undone. confirm must be the library's name. " +
			"Library 1 can't be deleted. The next blob cleanup removes images nothing else uses.",
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *deleteLibraryIn) (*jobResp, error) {
			l, err := s.Lib.GetLibrary(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			if l.ID == library.DefaultLibrary {
				return nil, huma.Error409Conflict("library 1 can't be deleted")
			}
			if in.Body.Confirm != l.Name {
				return nil, huma.Error422UnprocessableEntity("confirm must be the library's name")
			}
			return s.enqueue(ctx, jobs.Spec{Kind: bulk.DeleteLibraryKind, Payload: bulk.DeleteLibraryPayload{Library: l.ID}})
		})
}
