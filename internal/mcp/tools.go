package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/davidgibbons/innkeeper/internal/tier"
)

type idIn struct {
	ID int64 `json:"id"`
}

type entityIn struct {
	Kind string `json:"kind" jsonschema:"card or lorebook"`
	ID   int64  `json:"id"`
}

// plural is the API path segment for kind.
func plural(kind string) (string, error) {
	switch kind {
	case "card":
		return "cards", nil
	case "lorebook":
		return "lorebooks", nil
	}
	return "", fmt.Errorf("kind must be card or lorebook, not %q", kind)
}

type searchIn struct {
	Query map[string]any `json:"query" jsonschema:"The search query, as the API's POST /search body: text, filter, sources (library, or catalog:<instance id> to search a catalog too), include, fields, page"`
}

type createIn struct {
	Data    map[string]any `json:"data" jsonschema:"The CCv3 data object. A card's may not contain character_book; create a lorebook and link it instead."`
	Message string         `json:"message,omitempty" jsonschema:"What changed, for the history"`
}

type updateIn struct {
	ID             int64          `json:"id"`
	BaseRevisionID int64          `json:"base_revision_id" jsonschema:"The head revision you read. If it moved, the update fails with 409: read again and redo the edit."`
	Data           map[string]any `json:"data" jsonschema:"The whole new CCv3 data object"`
	Overrides      map[string]any `json:"overrides,omitempty" jsonschema:"JSON merge patches by plugin instance ID. Omit to keep them."`
	Message        string         `json:"message,omitempty" jsonschema:"What changed, for the history"`
}

type linkIn struct {
	CardID     int64 `json:"card_id"`
	LorebookID int64 `json:"lorebook_id"`
}

type restoreIn struct {
	Kind           string `json:"kind" jsonschema:"card or lorebook"`
	ID             int64  `json:"id"`
	RevisionID     int64  `json:"revision_id" jsonschema:"The old revision to bring back"`
	BaseRevisionID int64  `json:"base_revision_id" jsonschema:"The current head revision"`
	Avatar         string `json:"avatar,omitempty" jsonschema:"keep (default) keeps the head's image; revision takes the old revision's, even none"`
}

type previewIn struct {
	Kind       string `json:"kind" jsonschema:"card or lorebook"`
	ID         int64  `json:"id"`
	InstanceID int64  `json:"instance_id" jsonschema:"The target plugin instance"`
}

type pushIn struct {
	Kind       string `json:"kind" jsonschema:"card or lorebook"`
	ID         int64  `json:"id"`
	InstanceID int64  `json:"instance_id" jsonschema:"The target plugin instance"`
	Confirm    string `json:"confirm" jsonschema:"The confirm value from preview_push. Show the owner the preview's changes first."`
	NewCopy    bool   `json:"new_copy,omitempty" jsonschema:"Make another copy on a target that can't update"`
}

type bindingIn struct {
	BindingID int64 `json:"binding_id"`
}

type adoptIn struct {
	BindingID      int64  `json:"binding_id"`
	Into           string `json:"into" jsonschema:"base: into the card or lorebook. override: into this target's override. ignore: stop reporting these changes."`
	BaseRevisionID int64  `json:"base_revision_id,omitempty" jsonschema:"The head revision; required unless into is ignore"`
}

type itemsIn struct {
	InstanceID int64  `json:"instance_id" jsonschema:"The target plugin instance"`
	Kind       string `json:"kind,omitempty" jsonschema:"card or lorebook; default card"`
}

type bindIn struct {
	CardID     int64  `json:"card_id,omitempty" jsonschema:"The card to bind; give this or lorebook_id"`
	LorebookID int64  `json:"lorebook_id,omitempty" jsonschema:"The lorebook to bind; give this or card_id"`
	InstanceID int64  `json:"instance_id" jsonschema:"The target plugin instance"`
	RemoteID   string `json:"remote_id" jsonschema:"The app item's remote_id, from list_app_items"`
}

type sourceIn struct {
	CardID     int64  `json:"card_id,omitempty" jsonschema:"The card; give this or lorebook_id"`
	LorebookID int64  `json:"lorebook_id,omitempty" jsonschema:"The lorebook; give this or card_id"`
	URL        string `json:"url,omitempty" jsonschema:"A page on a known site, such as a chub.ai character page"`
	SourceID   int64  `json:"source_id,omitempty" jsonschema:"The source to remove, from list_sources"`
}

// sourcesPath is the API path of a card's or lorebook's sources.
func sourcesPath(in sourceIn) (string, error) {
	switch {
	case (in.CardID == 0) == (in.LorebookID == 0):
		return "", fmt.Errorf("give exactly one of card_id and lorebook_id")
	case in.CardID != 0:
		return fmt.Sprintf("/cards/%d/sources", in.CardID), nil
	}
	return fmt.Sprintf("/lorebooks/%d/sources", in.LorebookID), nil
}

type importIn struct {
	InstanceID int64    `json:"instance_id" jsonschema:"An import plugin or target app instance"`
	Path       string   `json:"path,omitempty" jsonschema:"For an import plugin: a subfolder of the instance's folder; empty for all of it"`
	RemoteIDs  []string `json:"remote_ids,omitempty" jsonschema:"For a target app: the items to import, from list_app_items; empty for every unbound one"`
}

type instanceIn struct {
	Plugin  string            `json:"plugin"`
	Name    string            `json:"name"`
	Config  map[string]any    `json:"config,omitempty"`
	Secrets map[string]string `json:"secrets,omitempty" jsonschema:"Write-only; never returned"`
}

type instanceUpdateIn struct {
	ID      int64              `json:"id"`
	Name    *string            `json:"name,omitempty"`
	Config  map[string]any     `json:"config,omitempty"`
	Secrets map[string]*string `json:"secrets,omitempty" jsonschema:"Sets each named secret; null removes one. Write-only."`
}

type reconcileIn struct {
	InstanceID  int64          `json:"instance_id" jsonschema:"The target plugin instance"`
	Match       map[string]any `json:"match,omitempty" jsonschema:"name (bool), text_distance and avatar_distance (0..32); defaults true, 8, 8"`
	SplitFields *int           `json:"split_fields,omitempty" jsonschema:"How many core fields must differ before import is suggested over bind; default 3"`
}

type compareIn struct {
	InstanceID int64  `json:"instance_id" jsonschema:"The target plugin instance"`
	RemoteID   string `json:"remote_id" jsonschema:"The app item's remote_id, from the reconcile snapshot"`
	Kind       string `json:"kind,omitempty" jsonschema:"card or lorebook; default card"`
	ID         int64  `json:"id,omitempty" jsonschema:"The library card or lorebook to compare with; omit for the bound one"`
}

type resolveIn struct {
	InstanceID     int64             `json:"instance_id" jsonschema:"The target plugin instance"`
	RemoteID       string            `json:"remote_id"`
	Kind           string            `json:"kind,omitempty" jsonschema:"card or lorebook; default card"`
	Action         string            `json:"action" jsonschema:"bind, import, or skip"`
	CardID         int64             `json:"card_id,omitempty" jsonschema:"For bind: the card or lorebook compared with"`
	AppHash        string            `json:"app_hash,omitempty" jsonschema:"From compare_item, or the snapshot for an import; not needed to skip"`
	BaseRevisionID int64             `json:"base_revision_id,omitempty" jsonschema:"For bind: head_revision_id from compare_item"`
	AcceptedHash   string            `json:"accepted_hash,omitempty" jsonschema:"accepted_hash from compare_item"`
	BindingID      int64             `json:"binding_id,omitempty" jsonschema:"For import: binding_id from compare_item or the snapshot item; omit for an unbound item"`
	Choices        map[string]string `json:"choices,omitempty" jsonschema:"Hunk ID, or avatar, to library, app, keep_in_app, or both"`
	Default        string            `json:"default,omitempty" jsonschema:"The choice for hunks not in choices; default library"`
	Lorebooks      []map[string]any  `json:"lorebooks,omitempty" jsonschema:"Resolutions for the character's linked world books, shaped like this one; left out ones take their suggestion"`
}

type resolveItemsIn struct {
	Resolutions []map[string]any `json:"resolutions" jsonschema:"Each shaped like resolve_item's input, with instance_id and kind"`
}

// itemPath is the API path of an app item, under kind's items.
func itemPath(instanceID int64, kind, remoteID string) (string, error) {
	items := "items"
	switch kind {
	case "", "card":
	case "lorebook":
		items = "lorebook-items"
	default:
		return "", fmt.Errorf("kind must be card or lorebook, not %q", kind)
	}
	return fmt.Sprintf("/instances/%d/%s/%s", instanceID, items, url.PathEscape(remoteID)), nil
}

type none struct{}

// tools are every built-in tool, each the API call it names.
var tools = []tool{
	def("search_cards", "Search the library's cards by text and filters. Returns matching cards.", tier.Read,
		func(ctx context.Context, c caller, in searchIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", "/search", in.Query)
		}),
	def("get_card", "Get a card: its head revision, tags, linked lorebooks, and its status on each target app.", tier.Read,
		func(ctx context.Context, c caller, in idIn) (*sdk.CallToolResult, error) {
			return c.both(ctx, fmt.Sprintf("/cards/%d", in.ID), fmt.Sprintf("/cards/%d/bindings", in.ID))
		}),
	def("get_lorebook", "Get a lorebook: its head revision and its status on each target app.", tier.Read,
		func(ctx context.Context, c caller, in idIn) (*sdk.CallToolResult, error) {
			return c.both(ctx, fmt.Sprintf("/lorebooks/%d", in.ID), fmt.Sprintf("/lorebooks/%d/bindings", in.ID))
		}),
	def("get_history", "List a card's or lorebook's revisions, newest first.", tier.Read,
		func(ctx context.Context, c caller, in entityIn) (*sdk.CallToolResult, error) {
			p, err := plural(in.Kind)
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "GET", fmt.Sprintf("/%s/%d/revisions", p, in.ID), nil)
		}),
	def("create_card", "Create a card from a CCv3 data object.", tier.Write,
		func(ctx context.Context, c caller, in createIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", "/cards", map[string]any{"data": in.Data, "message": in.Message, "source": "mcp"})
		}),
	def("create_lorebook", "Create a lorebook from a CCv3 character_book object.", tier.Write,
		func(ctx context.Context, c caller, in createIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", "/lorebooks", map[string]any{"data": in.Data, "message": in.Message, "source": "mcp"})
		}),
	def("update_card", "Save a new revision of a card. Send the whole data object, based on the head revision you read.", tier.Write,
		func(ctx context.Context, c caller, in updateIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "PUT", fmt.Sprintf("/cards/%d", in.ID), update(in))
		}),
	def("update_lorebook", "Save a new revision of a lorebook. Send the whole data object, based on the head revision you read.", tier.Write,
		func(ctx context.Context, c caller, in updateIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "PUT", fmt.Sprintf("/lorebooks/%d", in.ID), update(in))
		}),
	def("link_lorebook", "Link a lorebook to a card.", tier.Write,
		func(ctx context.Context, c caller, in linkIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "PUT", fmt.Sprintf("/cards/%d/lorebooks/%d", in.CardID, in.LorebookID), nil)
		}),
	def("unlink_lorebook", "Unlink a lorebook from a card.", tier.Write,
		func(ctx context.Context, c caller, in linkIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "DELETE", fmt.Sprintf("/cards/%d/lorebooks/%d", in.CardID, in.LorebookID), nil)
		}),
	def("restore_revision", "Bring back an old revision of a card or lorebook as a new revision.", tier.Write,
		func(ctx context.Context, c caller, in restoreIn) (*sdk.CallToolResult, error) {
			p, err := plural(in.Kind)
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "POST", fmt.Sprintf("/%s/%d/restore", p, in.ID),
				map[string]any{"revision_id": in.RevisionID, "base_revision_id": in.BaseRevisionID, "avatar": cmp.Or(in.Avatar, "keep"), "source": "mcp"})
		}),
	def("preview_push", "Show what pushing a card or lorebook to a target app would do: the fields it would change in the app, "+
		"with the app's value as before and the pushed one as after, fields it would drop, and lorebooks it would create. "+
		"Show the owner the changes, then pass confirm to push.", tier.Read,
		func(ctx context.Context, c caller, in previewIn) (*sdk.CallToolResult, error) {
			p, err := plural(in.Kind)
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "GET", fmt.Sprintf("/%s/%d/push-preview?instance_id=%d", p, in.ID, in.InstanceID), nil)
		}),
	def("push", "Push a card or lorebook to a target app, overwriting the app's copy. Needs confirm from preview_push; "+
		"if anything changed since, it returns the new preview instead.", tier.Destructive,
		func(ctx context.Context, c caller, in pushIn) (*sdk.CallToolResult, error) {
			p, err := plural(in.Kind)
			if err != nil {
				return nil, err
			}
			if in.Confirm == "" {
				return nil, fmt.Errorf("confirm is required: call preview_push and show the owner its changes first")
			}
			return c.call(ctx, "POST", fmt.Sprintf("/%s/%d/push", p, in.ID),
				map[string]any{"instance_id": in.InstanceID, "confirm": in.Confirm, "new_copy": in.NewCopy})
		}),
	def("check_drift", "Read a bound card or lorebook back from its app now, and report what changed there.", tier.Read,
		func(ctx context.Context, c caller, in bindingIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", fmt.Sprintf("/bindings/%d/check", in.BindingID), nil)
		}),
	def("adopt", "Take the changes made in the app into the library, as a new revision.", tier.Write,
		func(ctx context.Context, c caller, in adoptIn) (*sdk.CallToolResult, error) {
			body := map[string]any{"into": in.Into}
			if in.BaseRevisionID != 0 {
				body["base_revision_id"] = in.BaseRevisionID
			}
			return c.call(ctx, "POST", fmt.Sprintf("/bindings/%d/adopt", in.BindingID), body)
		}),
	def("list_bindings", "List the target apps a card or lorebook is pushed to, with each one's status.", tier.Read,
		func(ctx context.Context, c caller, in entityIn) (*sdk.CallToolResult, error) {
			p, err := plural(in.Kind)
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "GET", fmt.Sprintf("/%s/%d/bindings", p, in.ID), nil)
		}),
	def("list_sources", "List the sites a card or lorebook was published on.", tier.Read,
		func(ctx context.Context, c caller, in sourceIn) (*sdk.CallToolResult, error) {
			path, err := sourcesPath(in)
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "GET", path, nil)
		}),
	def("add_source", "Record that a card or lorebook was published at a URL, such as its chub.ai page.", tier.Write,
		func(ctx context.Context, c caller, in sourceIn) (*sdk.CallToolResult, error) {
			path, err := sourcesPath(in)
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "PUT", path, map[string]any{"url": in.URL})
		}),
	def("remove_source", "Remove a source from a card or lorebook, so detection doesn't add it back.", tier.Write,
		func(ctx context.Context, c caller, in sourceIn) (*sdk.CallToolResult, error) {
			path, err := sourcesPath(in)
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "DELETE", fmt.Sprintf("%s/%d", path, in.SourceID), nil)
		}),
	def("list_app_items", "List the cards or lorebooks a target app already has, with the binding that uses each, if any.", tier.Read,
		func(ctx context.Context, c caller, in itemsIn) (*sdk.CallToolResult, error) {
			path := "items"
			switch in.Kind {
			case "", "card":
			case "lorebook":
				path = "lorebook-items"
			default:
				return nil, fmt.Errorf("kind must be card or lorebook, not %q", in.Kind)
			}
			return c.call(ctx, "GET", fmt.Sprintf("/instances/%d/%s", in.InstanceID, path), nil)
		}),
	def("bind", "Link a card or lorebook to one the app already has. Changes nothing in the app or the library; the binding shows ahead until the next push where they differ. The app's linked world books are left for reconcile.", tier.Write,
		func(ctx context.Context, c caller, in bindIn) (*sdk.CallToolResult, error) {
			if (in.CardID == 0) == (in.LorebookID == 0) {
				return nil, fmt.Errorf("give exactly one of card_id and lorebook_id")
			}
			path := fmt.Sprintf("/cards/%d/bind", in.CardID)
			if in.LorebookID != 0 {
				path = fmt.Sprintf("/lorebooks/%d/bind", in.LorebookID)
			}
			return c.call(ctx, "POST", path, map[string]any{"instance_id": in.InstanceID, "remote_id": in.RemoteID})
		}),
	def("list_jobs", "List recent jobs: imports, pushes, scans, reconciles, bulk actions. A finished reconcile shows only its totals; get_job returns the whole snapshot.", tier.Read,
		func(ctx context.Context, c caller, _ none) (*sdk.CallToolResult, error) {
			return c.call(ctx, "GET", "/jobs", nil)
		}),
	def("get_job", "Get a job's status and progress.", tier.Read,
		func(ctx context.Context, c caller, in idIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "GET", fmt.Sprintf("/jobs/%d", in.ID), nil)
		}),
	def("cancel_job", "Cancel a job that hasn't finished.", tier.Write,
		func(ctx context.Context, c caller, in idIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", fmt.Sprintf("/jobs/%d/cancel", in.ID), nil)
		}),
	def("start_import", "Import cards and lorebooks from an import instance's folder, or from a target app, binding each "+
		"to the app item it came from. A card already in the library is bound rather than copied.", tier.Write,
		func(ctx context.Context, c caller, in importIn) (*sdk.CallToolResult, error) {
			body := map[string]any{"instance_id": in.InstanceID}
			switch {
			case in.Path != "":
				body["params"] = map[string]any{"path": in.Path}
			case in.RemoteIDs != nil:
				body["params"] = map[string]any{"remote_ids": in.RemoteIDs}
			}
			return c.call(ctx, "POST", "/imports", body)
		}),
	def("start_reconcile", "Line a target app up against the library. Starts a job; its finished get_job progress is the snapshot: "+
		"a verdict, candidates, and a suggested action for each app item. Changes nothing unless the instance has auto_bind_exact.", tier.Read,
		func(ctx context.Context, c caller, in reconcileIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", "/reconciles", in)
		}),
	def("compare_item", "Compare an app item with a library card or lorebook, hunk by hunk. resolve_item needs this reply's app_hash, "+
		"head_revision_id, accepted_hash, and binding_id. An unbound item needs an id to compare with.", tier.Read,
		func(ctx context.Context, c caller, in compareIn) (*sdk.CallToolResult, error) {
			path, err := itemPath(in.InstanceID, in.Kind, in.RemoteID)
			if err != nil {
				return nil, err
			}
			q := "card_id"
			if in.Kind == "lorebook" {
				q = "lorebook_id"
			}
			if in.ID != 0 {
				path += fmt.Sprintf("/compare?%s=%d", q, in.ID)
			} else {
				path += "/compare"
			}
			return c.call(ctx, "GET", path, nil)
		}),
	def("resolve_item", "Bind an app item to a library card with per-hunk choices, import it, or skip it. Show the owner compare_item first. "+
		"Changes the library and the binding, never the app: push afterwards to send the result. A 409 means something changed; it has a fresh comparison.", tier.Write,
		func(ctx context.Context, c caller, in resolveIn) (*sdk.CallToolResult, error) {
			path, err := itemPath(in.InstanceID, in.Kind, in.RemoteID)
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "POST", path+"/resolve", in)
		}),
	def("resolve_items", "Resolve many app items as one bulk job, such as a reconcile's suggestions. One that fails doesn't stop the others; "+
		"get_job on each child shows its outcome. Pushing is a separate step.", tier.Write,
		func(ctx context.Context, c caller, in resolveItemsIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", "/resolutions", in)
		}),
	def("list_plugins", "List the installed plugins, with each one's capabilities, config schema, and secret names.", tier.Read,
		func(ctx context.Context, c caller, _ none) (*sdk.CallToolResult, error) {
			return c.call(ctx, "GET", "/plugins", nil)
		}),
	def("list_instances", "List configured plugin instances. Secrets show by name only.", tier.Read,
		func(ctx context.Context, c caller, _ none) (*sdk.CallToolResult, error) {
			return c.call(ctx, "GET", "/instances", nil)
		}),
	def("create_instance", "Configure a new plugin instance, such as a target app or an importer. Not login providers.", tier.Destructive,
		func(ctx context.Context, c caller, in instanceIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", "/instances", in)
		}),
	def("update_instance", "Change a plugin instance's name, config, or secrets. Its plugin restarts with them. Login providers can't be changed here.", tier.Destructive,
		func(ctx context.Context, c caller, in instanceUpdateIn) (*sdk.CallToolResult, error) {
			body := struct {
				Name    *string            `json:"name,omitempty"`
				Config  map[string]any     `json:"config,omitempty"`
				Secrets map[string]*string `json:"secrets,omitempty"`
			}{in.Name, in.Config, in.Secrets}
			return c.call(ctx, "PATCH", fmt.Sprintf("/instances/%d", in.ID), body)
		}),
}

func update(in updateIn) map[string]any {
	body := map[string]any{"base_revision_id": in.BaseRevisionID, "data": in.Data, "message": in.Message, "source": "mcp"}
	if in.Overrides != nil {
		body["overrides"] = in.Overrides
	}
	return body
}

// both returns an entity and its bindings as one result.
func (c caller) both(ctx context.Context, entity, bindings string) (*sdk.CallToolResult, error) {
	e, err := c.do(ctx, "GET", entity, nil)
	if err != nil || !e.ok() {
		return result(e), err
	}
	b, err := c.do(ctx, "GET", bindings, nil)
	if err != nil || !b.ok() {
		return result(b), err
	}
	raw, err := json.Marshal(map[string]json.RawMessage{"entity": e.body, "bindings": b.body})
	if err != nil {
		return nil, err
	}
	return result(reply{e.status, raw}), nil
}
