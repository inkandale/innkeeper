package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/tier"
	"github.com/davidgibbons/innkeeper/protocol"
)

// itemCallTimeout bounds an items call; an import may fetch a page.
const itemCallTimeout = 2 * time.Minute

// itemTypeIn names an instance's item type. Huma reads params only from
// exported fields, so the inputs below repeat these rather than embed them.
type itemTypeIn struct {
	ID   int64  `path:"id"`
	Type string `path:"type" doc:"An item type the plugin's manifest declares"`
}

type itemIn struct {
	ID   int64  `path:"id"`
	Type string `path:"type"`
	Item string `path:"item"`
}

type itemBody struct {
	Item map[string]any `json:"item" doc:"The whole item, fitting the item type's schema"`
}

type createItemIn struct {
	ID   int64  `path:"id"`
	Type string `path:"type"`
	Body itemBody
}

type replaceItemIn struct {
	ID   int64  `path:"id"`
	Type string `path:"type"`
	Item string `path:"item"`
	Body itemBody
}

type importFileIn struct {
	ID      int64  `path:"id"`
	Type    string `path:"type"`
	Name    string `query:"name" required:"true" doc:"The file's name, such as SKILL.md"`
	RawBody []byte
}

type importURLIn struct {
	ID   int64  `path:"id"`
	Type string `path:"type"`
	Body struct {
		URL string `json:"url" minLength:"1"`
	}
}

type itemSummariesOut struct{ Body []protocol.ItemSummary }

type itemOut struct {
	Body struct {
		ID   string          `json:"id"`
		Item json.RawMessage `json:"item"`
	}
}

type itemIDOut struct {
	Body protocol.ItemsPutResult
}

func (s *Server) itemRoutes(api huma.API) {
	tags := []string{"Plugin items"}
	base := "/instances/{id}/items/{type}"
	huma.Register(api, huma.Operation{OperationID: "list-items", Method: http.MethodGet, Path: base,
		Summary: "List a plugin instance's items of one type", Tags: tags},
		func(ctx context.Context, in *itemTypeIn) (*itemSummariesOut, error) {
			var res protocol.ItemsListResult
			if err := s.callItems(ctx, in, protocol.MethodItemsList, protocol.ItemsListParams{Type: in.Type}, &res); err != nil {
				return nil, s.fail(err)
			}
			return &itemSummariesOut{Body: s.validSummaries(in.ID, res.Items)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-item", Method: http.MethodGet, Path: base + "/{item}",
		Summary: "Get one of a plugin instance's items", Tags: tags},
		func(ctx context.Context, in *itemIn) (*itemOut, error) {
			var res protocol.ItemsGetResult
			if err := s.callItems(ctx, &itemTypeIn{in.ID, in.Type}, protocol.MethodItemsGet, protocol.ItemRef{Type: in.Type, ID: in.Item}, &res); err != nil {
				return nil, s.fail(err)
			}
			out := &itemOut{}
			out.Body.ID, out.Body.Item = in.Item, res.Item
			return out, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "create-item", Method: http.MethodPost, Path: base,
		Summary: "Add an item to a plugin instance", Tags: tags, DefaultStatus: http.StatusCreated,
		Description: "A 400 carries the plugin's reason for refusing the item."},
		func(ctx context.Context, in *createItemIn) (*itemIDOut, error) {
			return s.putItem(ctx, &itemTypeIn{in.ID, in.Type}, "", in.Body.Item)
		})

	// Destructive, like the instance's settings: the old item is gone.
	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "replace-item", Method: http.MethodPut,
		Path: base + "/{item}", Summary: "Replace one of a plugin instance's items", Tags: tags,
		Description: "The reply's id may differ from the path's, such as for a renamed skill. A 400 carries the plugin's reason for refusing the item."},
		func(ctx context.Context, in *replaceItemIn) (*itemIDOut, error) {
			return s.putItem(ctx, &itemTypeIn{in.ID, in.Type}, in.Item, in.Body.Item)
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "delete-item", Method: http.MethodDelete,
		Path: base + "/{item}", Summary: "Delete one of a plugin instance's items", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *itemIn) (*struct{}, error) {
			return nil, s.fail(s.callItems(ctx, &itemTypeIn{in.ID, in.Type}, protocol.MethodItemsDelete, protocol.ItemRef{Type: in.Type, ID: in.Item}, nil))
		})

	// Destructive: an import may replace items with the same IDs.
	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "import-item-file", Method: http.MethodPost,
		Path: base + "/import", Summary: "Import items from a file", Tags: tags,
		Description:  "Send the file as application/octet-stream. Returns what was imported. A 400 carries the plugin's reason for refusing the file.",
		MaxBodyBytes: library.MaxBlobBytes + 1, BodyReadTimeout: 2 * time.Minute},
		func(ctx context.Context, in *importFileIn) (*itemSummariesOut, error) {
			return s.importItems(ctx, &itemTypeIn{in.ID, in.Type}, "file", protocol.ItemsImportParams{Type: in.Type, Name: in.Name}, in.RawBody)
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "import-item-url", Method: http.MethodPost,
		Path: base + "/import-url", Summary: "Import items from a URL", Tags: tags,
		Description: "Returns what was imported. A 400 carries the plugin's reason for refusing the URL."},
		func(ctx context.Context, in *importURLIn) (*itemSummariesOut, error) {
			return s.importItems(ctx, &itemTypeIn{in.ID, in.Type}, "url", protocol.ItemsImportParams{Type: in.Type, URL: in.Body.URL}, nil)
		})
}

// importItems sends items.import, with file written to blob_tmp as params'
// path when there is one. input is what the item type must import from.
func (s *Server) importItems(ctx context.Context, in *itemTypeIn, input string, params protocol.ItemsImportParams, file []byte) (*itemSummariesOut, error) {
	inst, err := s.itemsInstance(ctx, in)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(inst.Manifest.Items[in.Type].Import, input) {
		return nil, huma.Error400BadRequest(fmt.Sprintf("item type %s doesn't import from a %s", in.Type, input))
	}
	if input == "file" {
		if params.Path, err = writeTmp(inst.Params.BlobTmp, file); err != nil {
			return nil, err
		}
		defer os.Remove(params.Path)
	}
	var res protocol.ItemsImportResult
	if err := s.callItemsOn(ctx, inst, protocol.MethodItemsImport, params, &res); err != nil {
		return nil, s.itemsFail(err)
	}
	return &itemSummariesOut{Body: s.validSummaries(in.ID, res.Items)}, nil
}

// itemsInstance is the instance in, if its plugin keeps in.Type.
func (s *Server) itemsInstance(ctx context.Context, in *itemTypeIn) (pluginhost.Instance, error) {
	inst, err := s.usable(ctx, in.ID, "items")
	if err != nil {
		return inst, err
	}
	if _, ok := inst.Manifest.Items[in.Type]; !ok {
		return inst, huma.Error404NotFound(fmt.Sprintf("plugin %s has no item type %q", inst.Manifest.Name, in.Type))
	}
	return inst, nil
}

// callItems sends method to the instance in names, after checking its type.
func (s *Server) callItems(ctx context.Context, in *itemTypeIn, method string, params, result any) error {
	inst, err := s.itemsInstance(ctx, in)
	if err != nil {
		return err
	}
	return s.callItemsOn(ctx, inst, method, params, result)
}

func (s *Server) callItemsOn(ctx context.Context, inst pluginhost.Instance, method string, params, result any) error {
	callCtx, cancel := context.WithTimeout(ctx, itemCallTimeout)
	defer cancel()
	return s.Registry.Call(callCtx, s.Plugins, inst, method, params, result)
}

func (s *Server) putItem(ctx context.Context, in *itemTypeIn, id string, item map[string]any) (*itemIDOut, error) {
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	out := &itemIDOut{}
	err = s.callItems(ctx, in, protocol.MethodItemsPut, protocol.ItemsPutParams{Type: in.Type, ID: id, Item: raw}, &out.Body)
	if err == nil {
		err = protocol.CheckItemID(out.Body.ID)
	}
	return out, s.itemsFail(err)
}

// itemsFail is fail, except that the owner sees why the plugin refused an
// item or an import: the owner sent it, and items.put and items.import
// need write or above.
func (s *Server) itemsFail(err error) error {
	var rpcErr *protocol.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeInvalidParams {
		return huma.Error400BadRequest(rpcErr.Message)
	}
	return s.fail(err)
}

// validSummaries drops and logs the summaries the UI couldn't show.
func (s *Server) validSummaries(instance int64, list []protocol.ItemSummary) []protocol.ItemSummary {
	out := []protocol.ItemSummary{}
	for _, it := range list {
		if err := it.Validate(); err != nil {
			s.Log.Warn("skip invalid item", "instance", instance, "err", err)
			continue
		}
		out = append(out, it)
	}
	return out
}

// writeTmp writes an upload to a new file directly inside blob_tmp.
func writeTmp(dir string, body []byte) (string, error) {
	f, err := os.CreateTemp(dir, "upload-*")
	if err != nil {
		return "", err
	}
	_, err = f.Write(body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
