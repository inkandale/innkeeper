package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/merge"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

type revisionOut struct {
	ID          int64           `json:"id"`
	ParentID    *int64          `json:"parent_id,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	Avatar      *string         `json:"avatar,omitempty"`
	Overrides   json.RawMessage `json:"overrides,omitempty"`
	ContentHash string          `json:"content_hash"`
	Message     string          `json:"message"`
	Source      string          `json:"source"`
	CreatedAt   time.Time       `json:"created_at"`
}

// toRevision converts a revision. History lists leave out data and
// overrides.
func toRevision(r library.Revision, full bool) revisionOut {
	out := revisionOut{ID: r.ID, ParentID: r.ParentID, Avatar: r.AvatarBlob, ContentHash: r.ContentHash,
		Message: r.Message, Source: r.Source, CreatedAt: r.CreatedAt}
	if full {
		out.Data, out.Overrides = r.Data, r.Overrides
	}
	return out
}

type entityOut struct {
	ID          int64       `json:"id"`
	Slug        string      `json:"slug"`
	Provenance  string      `json:"provenance"`
	ArchivedAt  *time.Time  `json:"archived_at,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	Revision    revisionOut `json:"revision" doc:"The head revision"`
	Tags        []string    `json:"tags,omitempty" doc:"Cards only"`
	Lorebooks   []int64     `json:"lorebooks,omitempty" doc:"Cards only: IDs of linked lorebooks"`
	Collections []int64     `json:"collections,omitempty" doc:"Cards only: IDs of the collections it's in"`
	Cards       []int64     `json:"cards,omitempty" doc:"Lorebooks only: IDs of linked cards"`
}

type entityResp struct{ Body entityOut }

type createEntityIn struct {
	Body struct {
		Slug    string          `json:"slug,omitempty" pattern:"^[a-z0-9]+(-[a-z0-9]+)*$" doc:"Default: made from data.name"`
		Data    json.RawMessage `json:"data" doc:"The CCv3 data object. A card's may not contain character_book; link a lorebook instead."`
		Avatar  string          `json:"avatar,omitempty" doc:"sha256 of an uploaded blob"`
		Message string          `json:"message,omitempty"`
		Source  string          `json:"source,omitempty" enum:"ui,mcp" default:"ui"`
	}
}

type updateEntityIn struct {
	ID   int64 `path:"id"`
	Body struct {
		BaseRevisionID int64           `json:"base_revision_id" doc:"The head revision this edit is based on. A stale one returns 409."`
		Data           json.RawMessage `json:"data"`
		Avatar         *string         `json:"avatar,omitempty" doc:"Omit to keep the base revision's avatar; empty to remove it."`
		Overrides      json.RawMessage `json:"overrides,omitempty" doc:"JSON merge patches by plugin instance ID. Omit to keep the base revision's."`
		Message        string          `json:"message,omitempty"`
		Source         string          `json:"source,omitempty" enum:"ui,mcp" default:"ui"`
	}
}

type revisionsOut struct{ Body []revisionOut }

type revisionIn struct {
	ID         int64 `path:"id"`
	RevisionID int64 `path:"revision_id"`
}

type revisionResp struct{ Body revisionOut }

type compareRevisionsIn struct {
	ID         int64 `path:"id"`
	RevisionID int64 `path:"revision_id"`
	OtherID    int64 `path:"other_id"`
}

type revisionDiff struct {
	Fields []merge.Field `json:"fields" doc:"Every field, sorted by path. In each hunk, library is revision_id's value and app is other_id's."`
}

type revisionDiffOut struct{ Body revisionDiff }

type restoreIn struct {
	ID   int64 `path:"id"`
	Body struct {
		RevisionID     int64  `json:"revision_id" doc:"The old revision to restore"`
		BaseRevisionID int64  `json:"base_revision_id" doc:"The current head revision"`
		Source         string `json:"source,omitempty" enum:"ui,mcp" default:"ui"`
	}
}

type archiveIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Archived bool `json:"archived"`
	}
}

type tagsIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Tags []string `json:"tags"`
	}
}

type tagsOut struct{ Body []string }

type sourcesOut struct{ Body []library.Source }

type sourceResp struct{ Body library.Source }

type addSourceIn struct {
	ID   int64 `path:"id"`
	Body struct {
		URL        string `json:"url,omitempty" doc:"A page on a known site. Use this, or kind and ref."`
		Kind       string `json:"kind,omitempty" doc:"The site, such as chub"`
		Ref        string `json:"ref,omitempty" doc:"The item's reference on that site, such as user/name"`
		ExternalID string `json:"external_id,omitempty"`
	}
}

type removeSourceIn struct {
	ID       int64 `path:"id"`
	SourceID int64 `path:"source_id"`
}

type similarIn struct {
	ID          int64 `path:"id"`
	MaxDistance int   `query:"max_distance" default:"8" minimum:"0" maximum:"32" doc:"Most differing hash bits to count as similar"`
}

type matchesOut struct{ Body []library.Match }

type linkIn struct {
	ID         int64 `path:"id"`
	LorebookID int64 `path:"lorebook_id"`
}

type exportIn struct {
	ID     int64  `path:"id"`
	Format string `query:"format" default:"json" doc:"json for CCv3 JSON, or a format a codec plugin instance writes, such as png"`
}

type exportOut struct {
	ContentDisposition string `header:"Content-Disposition"`
	ContentType        string `header:"Content-Type"`
	NoSniff            string `header:"X-Content-Type-Options"`
	CSP                string `header:"Content-Security-Policy"`
	Body               []byte
}

// nameOf returns data.name, for making a slug.
func nameOf(data json.RawMessage) string {
	var d struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(data, &d)
	return d.Name
}

// entity returns an entity with its head revision, and a card's tags and
// lorebooks.
func (s *Server) entity(ctx context.Context, ents *library.Entities, id int64) (*entityResp, error) {
	e, err := ents.Get(ctx, id)
	if err != nil {
		return nil, s.fail(err)
	}
	rev, err := ents.Revision(ctx, e.HeadRevisionID)
	if err != nil {
		return nil, s.fail(err)
	}
	out := &entityResp{Body: entityOut{ID: e.ID, Slug: e.Slug, Provenance: e.Provenance, ArchivedAt: e.ArchivedAt,
		CreatedAt: e.CreatedAt, Revision: toRevision(rev, true)}}
	if ents == s.Lib.Cards {
		if out.Body.Tags, err = s.Lib.CardTags(ctx, id); err != nil {
			return nil, s.fail(err)
		}
		if out.Body.Lorebooks, err = s.Lib.CardLorebooks(ctx, id); err != nil {
			return nil, s.fail(err)
		}
		if out.Body.Collections, err = s.Lib.CardCollections(ctx, id); err != nil {
			return nil, s.fail(err)
		}
	}
	if ents == s.Lib.Lorebooks {
		if out.Body.Cards, err = s.Lib.LorebookCards(ctx, id); err != nil {
			return nil, s.fail(err)
		}
	}
	return out, nil
}

// revisionOf returns a revision if it belongs to entity id.
func (s *Server) revisionOf(ctx context.Context, ents *library.Entities, id, revID int64) (library.Revision, error) {
	r, err := ents.Revision(ctx, revID)
	if err == nil && r.EntityID != id {
		err = library.ErrNotFound
	}
	if err != nil {
		return r, s.fail(err)
	}
	return r, nil
}

// entityRoutes registers the routes cards and lorebooks share, under
// /<plural>.
func (s *Server) entityRoutes(api huma.API, ents *library.Entities, plural, singular, tag string) {
	tags := []string{tag}
	base := "/" + plural
	item := base + "/{id}"

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "create-" + singular, Method: http.MethodPost, Path: base,
		Summary: "Create a " + singular, Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *createEntityIn) (*entityResp, error) {
			e := library.Edit{Data: in.Body.Data, AvatarBlob: in.Body.Avatar, Message: in.Body.Message, Source: in.Body.Source}
			var ent library.Entity
			var err error
			if in.Body.Slug != "" {
				ent, _, err = ents.Create(ctx, in.Body.Slug, "api", e)
			} else {
				ent, _, err = ents.CreateFromName(ctx, nameOf(in.Body.Data), "api", e)
			}
			if err != nil {
				return nil, s.fail(err)
			}
			return s.entity(ctx, ents, ent.ID)
		})

	huma.Register(api, huma.Operation{OperationID: "get-" + singular, Method: http.MethodGet, Path: item,
		Summary: "Get a " + singular, Tags: tags},
		func(ctx context.Context, in *idIn) (*entityResp, error) {
			return s.entity(ctx, ents, in.ID)
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "update-" + singular, Method: http.MethodPut, Path: item,
		Summary: "Edit a " + singular, Description: "Adds a revision on top of base_revision_id.", Tags: tags},
		func(ctx context.Context, in *updateEntityIn) (*entityResp, error) {
			if _, err := ents.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			base, err := ents.Revision(ctx, in.Body.BaseRevisionID)
			if errors.Is(err, library.ErrNotFound) || err == nil && base.EntityID != in.ID {
				return nil, s.fail(library.ErrConflict)
			}
			if err != nil {
				return nil, s.fail(err)
			}
			e := library.Edit{Data: in.Body.Data, Overrides: in.Body.Overrides, Message: in.Body.Message, Source: in.Body.Source}
			if e.Overrides == nil || string(e.Overrides) == "null" {
				e.Overrides = base.Overrides
			}
			if in.Body.Avatar != nil {
				e.AvatarBlob = *in.Body.Avatar
			} else if base.AvatarBlob != nil {
				e.AvatarBlob = *base.AvatarBlob
			}
			if _, err := ents.Update(ctx, in.ID, base.ID, e); err != nil {
				return nil, s.fail(err)
			}
			return s.entity(ctx, ents, in.ID)
		})

	huma.Register(api, huma.Operation{OperationID: "list-" + singular + "-revisions", Method: http.MethodGet,
		Path: item + "/revisions", Summary: "List a " + singular + "'s revisions", Description: "Newest first, without data.", Tags: tags},
		func(ctx context.Context, in *idIn) (*revisionsOut, error) {
			if _, err := ents.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			revs, err := ents.History(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			out := &revisionsOut{Body: make([]revisionOut, len(revs))}
			for i, r := range revs {
				out.Body[i] = toRevision(r, false)
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-" + singular + "-revision", Method: http.MethodGet,
		Path: item + "/revisions/{revision_id}", Summary: "Get one revision of a " + singular, Tags: tags},
		func(ctx context.Context, in *revisionIn) (*revisionResp, error) {
			r, err := s.revisionOf(ctx, ents, in.ID, in.RevisionID)
			if err != nil {
				return nil, err
			}
			return &revisionResp{Body: toRevision(r, true)}, nil
		})

	schema := merge.Card
	if ents == s.Lib.Lorebooks {
		schema = merge.Lorebook
	}
	huma.Register(api, huma.Operation{OperationID: "compare-" + singular + "-revisions", Method: http.MethodGet,
		Path: item + "/revisions/{revision_id}/compare/{other_id}", Summary: "Compare two revisions of a " + singular, Tags: tags},
		func(ctx context.Context, in *compareRevisionsIn) (*revisionDiffOut, error) {
			a, err := s.revisionOf(ctx, ents, in.ID, in.RevisionID)
			if err != nil {
				return nil, err
			}
			b, err := s.revisionOf(ctx, ents, in.ID, in.OtherID)
			if err != nil {
				return nil, err
			}
			fields, err := merge.Compare(schema, a.Data, b.Data, nil)
			if err != nil {
				return nil, s.fail(err)
			}
			return &revisionDiffOut{Body: revisionDiff{Fields: fields}}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "restore-" + singular, Method: http.MethodPost,
		Path: item + "/restore", Summary: "Restore an old revision",
		Description: "Adds a revision with the old revision's content on top of base_revision_id.", Tags: tags},
		func(ctx context.Context, in *restoreIn) (*entityResp, error) {
			old, err := s.revisionOf(ctx, ents, in.ID, in.Body.RevisionID)
			if err != nil {
				return nil, err
			}
			e := library.Edit{Data: old.Data, Overrides: old.Overrides, Source: cmp.Or(in.Body.Source, "ui"),
				Message: fmt.Sprintf("Restore revision %d", old.ID)}
			if old.AvatarBlob != nil {
				e.AvatarBlob = *old.AvatarBlob
			}
			if _, err := ents.Update(ctx, in.ID, in.Body.BaseRevisionID, e); err != nil {
				return nil, s.fail(err)
			}
			return s.entity(ctx, ents, in.ID)
		})

	huma.Register(api, huma.Operation{OperationID: "list-" + singular + "-sources", Method: http.MethodGet,
		Path: item + "/sources", Summary: "List the sites a " + singular + " was published on", Tags: tags},
		func(ctx context.Context, in *idIn) (*sourcesOut, error) {
			if _, err := ents.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			src, err := ents.Sources(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &sourcesOut{Body: orEmpty(src)}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "add-" + singular + "-source", Method: http.MethodPut,
		Path: item + "/sources", Summary: "Link a " + singular + " to a site",
		Description: "Takes a url, or a kind and ref. Restores a source that was removed.", Tags: tags},
		func(ctx context.Context, in *addSourceIn) (*sourceResp, error) {
			b := in.Body
			if b.URL != "" && (b.Kind != "" || b.Ref != "" || b.ExternalID != "") {
				return nil, huma.Error422UnprocessableEntity("give a url, or a kind and ref, not both")
			}
			lorebook := ents == s.Lib.Lorebooks
			src := library.Source{Kind: b.Kind, Ref: b.Ref, ExternalID: b.ExternalID}
			if b.URL != "" {
				var err error
				if src, err = library.ParseSourceURL(b.URL); err != nil {
					return nil, huma.Error422UnprocessableEntity(err.Error())
				}
			}
			// Rebuilt so the stored URL always matches the entity type.
			src, err := library.ValidateSource(src, lorebook)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			got, err := ents.AddSource(ctx, in.ID, src)
			if err != nil {
				return nil, s.fail(err)
			}
			return &sourceResp{Body: got}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "remove-" + singular + "-source", Method: http.MethodDelete,
		Path: item + "/sources/{source_id}", Summary: "Remove a " + singular + "'s source",
		Description: "Detection won't add it back.", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *removeSourceIn) (*struct{}, error) {
			return nil, s.fail(ents.RemoveSource(ctx, in.ID, in.SourceID))
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "archive-" + singular, Method: http.MethodPut,
		Path: item + "/archived", Summary: "Archive or unarchive a " + singular, Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *archiveIn) (*struct{}, error) {
			return nil, s.fail(ents.SetArchived(ctx, in.ID, in.Body.Archived))
		})
}

// cardRoutes registers the routes only cards have.
func (s *Server) cardRoutes(api huma.API) {
	tags := []string{"Cards"}
	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "set-card-tags", Method: http.MethodPut, Path: "/cards/{id}/tags",
		Summary: "Replace a card's tags", Tags: tags},
		func(ctx context.Context, in *tagsIn) (*tagsOut, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			if err := s.Lib.SetCardTags(ctx, in.ID, in.Body.Tags); err != nil {
				return nil, s.fail(err)
			}
			tags, err := s.Lib.CardTags(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &tagsOut{Body: orEmpty(tags)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-similar-cards", Method: http.MethodGet, Path: "/cards/{id}/similar",
		Summary: "Find likely duplicates", Description: "Same content, near-identical text, or a similar avatar; closest first.", Tags: tags},
		func(ctx context.Context, in *similarIn) (*matchesOut, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			m, err := s.Lib.SimilarCards(ctx, in.ID, in.MaxDistance)
			if err != nil {
				return nil, s.fail(err)
			}
			return &matchesOut{Body: orEmpty(m)}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "link-lorebook", Method: http.MethodPut,
		Path: "/cards/{id}/lorebooks/{lorebook_id}", Summary: "Link a lorebook to a card", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *linkIn) (*struct{}, error) {
			return nil, s.fail(s.Lib.LinkLorebook(ctx, in.ID, in.LorebookID))
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "unlink-lorebook", Method: http.MethodDelete,
		Path: "/cards/{id}/lorebooks/{lorebook_id}", Summary: "Unlink a lorebook from a card", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *linkIn) (*struct{}, error) {
			return nil, s.fail(s.Lib.UnlinkLorebook(ctx, in.ID, in.LorebookID))
		})

	huma.Register(api, huma.Operation{OperationID: "export-card", Method: http.MethodGet, Path: "/cards/{id}/export",
		Summary: "Export a card", Description: "Linked lorebooks are embedded as data.character_book. Formats other than json need a codec plugin instance that writes them.", Tags: tags,
		// Body is the raw file, not the base64 string huma assumes for []byte.
		Responses: map[string]*huma.Response{"200": {Description: "The card file",
			Content: map[string]*huma.MediaType{
				"application/json":         {Schema: &huma.Schema{Type: huma.TypeObject, Description: "A CCv3 card"}},
				"application/octet-stream": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}}}}},
		func(ctx context.Context, in *exportIn) (*exportOut, error) {
			f, err := s.exporter().Card(ctx, in.ID, in.Format)
			if err != nil {
				return nil, s.fail(err)
			}
			return &exportOut{ContentDisposition: fmt.Sprintf(`attachment; filename="%s"`, f.Name),
				ContentType: exportType(f.Type), NoSniff: "nosniff", CSP: "sandbox", Body: f.Body}, nil
		})
}
