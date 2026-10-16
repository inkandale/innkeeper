package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/tier"
)

type tagCountsOut struct{ Body []library.TagCount }

type collectionsOut struct{ Body []library.Collection }

type collectionOut struct{ Body library.Collection }

type createCollectionIn struct {
	Body struct {
		Name        string `json:"name" minLength:"1"`
		Description string `json:"description,omitempty"`
	}
}

type updateCollectionIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Name        *string `json:"name,omitempty" minLength:"1"`
		Description *string `json:"description,omitempty"`
	}
}

type collectionCardIn struct {
	ID     int64 `path:"id"`
	CardID int64 `path:"card_id"`
}

type uploadIn struct {
	RawBody []byte
}

type blobOut struct{ Body library.Blob }

type blobIn struct {
	SHA256 string `path:"sha256" pattern:"^[0-9a-f]{64}$"`
	W      int    `query:"w" doc:"Return a JPEG of an image this many pixels wide: 160, 320, or 640."`
}

func (s *Server) organizeRoutes(api huma.API) {
	tags := []string{"Organize"}
	huma.Register(api, huma.Operation{OperationID: "list-tags", Method: http.MethodGet, Path: "/tags",
		Summary: "List tags with card counts", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*tagCountsOut, error) {
			t, err := s.Lib.Tags(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &tagCountsOut{Body: orEmpty(t)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-collections", Method: http.MethodGet, Path: "/collections",
		Summary: "List collections with card counts", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*collectionsOut, error) {
			c, err := s.Lib.Collections(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &collectionsOut{Body: orEmpty(c)}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "create-collection", Method: http.MethodPost, Path: "/collections",
		Summary: "Create a collection", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *createCollectionIn) (*collectionOut, error) {
			c, err := s.Lib.CreateCollection(ctx, in.Body.Name, in.Body.Description)
			if err != nil {
				return nil, s.fail(err)
			}
			return &collectionOut{Body: c}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "update-collection", Method: http.MethodPatch, Path: "/collections/{id}",
		Summary: "Rename a collection or change its description", Tags: tags},
		func(ctx context.Context, in *updateCollectionIn) (*collectionOut, error) {
			c, err := s.Lib.UpdateCollection(ctx, in.ID, in.Body.Name, in.Body.Description)
			if err != nil {
				return nil, s.fail(err)
			}
			return &collectionOut{Body: c}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "delete-collection", Method: http.MethodDelete, Path: "/collections/{id}",
		Summary: "Delete a collection", Description: "Its cards stay in the library.", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			return nil, s.fail(s.Lib.DeleteCollection(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "add-to-collection", Method: http.MethodPut,
		Path: "/collections/{id}/cards/{card_id}", Summary: "Add a card to a collection", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *collectionCardIn) (*struct{}, error) {
			return nil, s.fail(s.Lib.AddToCollection(ctx, in.ID, in.CardID))
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "remove-from-collection", Method: http.MethodDelete,
		Path: "/collections/{id}/cards/{card_id}", Summary: "Remove a card from a collection", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *collectionCardIn) (*struct{}, error) {
			return nil, s.fail(s.Lib.RemoveFromCollection(ctx, in.ID, in.CardID))
		})

	tags = []string{"Blobs"}
	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "upload-blob", Method: http.MethodPost, Path: "/blobs",
		Summary: "Upload a file", Description: "Send any file as application/octet-stream. Uploading the same bytes twice returns the same blob.",
		Tags: tags, DefaultStatus: http.StatusCreated, MaxBodyBytes: library.MaxBlobBytes + 1, BodyReadTimeout: 2 * time.Minute,
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/octet-stream": {}}}},
		func(ctx context.Context, in *uploadIn) (*blobOut, error) {
			b, err := s.Lib.PutBlob(ctx, bytes.NewReader(in.RawBody))
			if err != nil {
				return nil, s.fail(err)
			}
			return &blobOut{Body: b}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-blob", Method: http.MethodGet, Path: "/blobs/{sha256}",
		Summary: "Download a file", Tags: tags},
		func(ctx context.Context, in *blobIn) (*huma.StreamResponse, error) {
			var f *os.File
			var mime string
			var err error
			if in.W != 0 {
				f, err = s.Lib.OpenThumb(ctx, in.SHA256, in.W)
				mime = "image/jpeg"
			} else {
				var b library.Blob
				b, f, err = s.Lib.OpenBlob(ctx, in.SHA256)
				mime = b.MIME
			}
			if err != nil {
				return nil, s.fail(err)
			}
			st, err := f.Stat()
			if err != nil {
				f.Close()
				return nil, s.fail(err)
			}
			return &huma.StreamResponse{Body: func(hc huma.Context) {
				defer f.Close()
				// Only images display inline. Anything else downloads, so an
				// uploaded HTML file can't run as this site.
				if !strings.HasPrefix(mime, "image/") {
					mime = "application/octet-stream"
				}
				hc.SetHeader("Content-Type", mime)
				hc.SetHeader("Content-Length", strconv.FormatInt(st.Size(), 10))
				hc.SetHeader("X-Content-Type-Options", "nosniff")
				hc.SetHeader("Content-Security-Policy", "sandbox")
				hc.SetHeader("Cache-Control", "private, max-age=31536000, immutable")
				if _, err := io.Copy(hc.BodyWriter(), f); err != nil {
					s.Log.Warn("send blob", "sha256", in.SHA256, "err", err)
				}
			}}, nil
		})
}
