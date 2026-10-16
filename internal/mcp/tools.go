package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

type idIn struct {
	ID int64 `json:"id"`
}

type entityIn struct {
	Kind string `json:"kind" jsonschema:"card or lorebook"`
	ID   int64  `json:"id"`
}

// kindNamed is the library kind a tool's input names.
func kindNamed(name string) (*library.Kind, error) {
	if k, ok := library.KindNamed(name); ok {
		return k, nil
	}
	return nil, fmt.Errorf("kind must be card or lorebook, not %q", name)
}

type searchIn struct {
	Query map[string]any `json:"query" jsonschema:"The search query, as the API's POST /cards/search body: text, filter, sources (library, or catalog:<instance id> to search a catalog too), include, fields, sort, page. A filter or sort on an enrichment, such as {\"enrichment\": \"quality\", \"gte\": 70} or sort enrichment:quality, uses the score instance's values"`
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

type forkIn struct {
	Kind string `json:"kind,omitempty" jsonschema:"card or lorebook; default card"`
	ID   int64  `json:"id"`
}

type variantOfIn struct {
	Kind      string `json:"kind,omitempty" jsonschema:"card or lorebook; default card"`
	ID        int64  `json:"id"`
	VariantOf *int64 `json:"variant_of,omitempty" jsonschema:"The card or lorebook of the same kind it is a variant of; null or left out unlinks it"`
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
	InstanceID int64  `json:"instance_id" jsonschema:"An instance of an import plugin"`
	Path       string `json:"path,omitempty" jsonschema:"A subfolder of the instance's folder; empty for all of it"`
	URL        string `json:"url,omitempty" jsonschema:"A card's or lorebook's page URL, for a url-import instance"`
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

type syncIn struct {
	InstanceID int64  `json:"instance_id,omitempty" jsonschema:"One target plugin instance to sync; omit to sync every app"`
	Apply      string `json:"apply,omitempty" jsonschema:"obvious (default) binds exact matches and imports new items; none writes nothing"`
}

type changesIn struct {
	InstanceID int64  `json:"instance_id,omitempty" jsonschema:"Only this target app's changes; counts still cover every app"`
	State      string `json:"state,omitempty" jsonschema:"Only this state: differs, both_changed, ahead, missing, error, new, or update. Ahead means the library has what the app doesn't: not pushed. Update means a followed site source changed"`
}

type sourceCheckIn struct {
	Kind string `json:"kind,omitempty" jsonschema:"card or lorebook; default card"`
	ID   int64  `json:"id,omitempty" jsonschema:"One card or lorebook to check now; omit to start a job that checks every source"`
}

type autosyncIn struct {
	BindingID int64 `json:"binding_id" jsonschema:"The binding, from list_bindings"`
	Autosync  bool  `json:"autosync" jsonschema:"true to turn autosync on, false to turn it off"`
}

type compareIn struct {
	InstanceID int64  `json:"instance_id" jsonschema:"The target plugin instance"`
	RemoteID   string `json:"remote_id" jsonschema:"The app item's remote_id, from the sync snapshot"`
	Kind       string `json:"kind,omitempty" jsonschema:"card or lorebook; default card"`
	ID         int64  `json:"id,omitempty" jsonschema:"The library card or lorebook to compare with; omit for the bound one"`
	TwoWay     bool   `json:"two_way,omitempty" jsonschema:"Cards only: compare without the binding's accepted copy, so every difference is a choice. Use it for an ahead card; resolve with base none"`
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
	Base           string            `json:"base,omitempty" jsonschema:"none when compare_item ran with two_way"`
	BindingID      int64             `json:"binding_id,omitempty" jsonschema:"For import: binding_id from compare_item or the snapshot item; omit for an unbound item"`
	Choices        map[string]string `json:"choices,omitempty" jsonschema:"Hunk ID, or avatar, to library, app, keep_in_app, or both"`
	Default        string            `json:"default,omitempty" jsonschema:"The choice for hunks not in choices; default library"`
	Lorebooks      []map[string]any  `json:"lorebooks,omitempty" jsonschema:"Resolutions for the character's linked world books, shaped like this one; left out ones take their suggestion"`
}

type resolveItemsIn struct {
	Resolutions []map[string]any `json:"resolutions" jsonschema:"Each shaped like resolve_item's input, with instance_id and kind"`
}

// itemPath is the API path of an app item of kind k.
func itemPath(instanceID int64, k *library.Kind, remoteID string) string {
	return fmt.Sprintf("/instances/%d/%s/%s", instanceID, k.Plural, url.PathEscape(remoteID))
}

type none struct{}

// tools are every built-in tool, each the API call it names.
var tools = []tool{
	def("search_cards", "Search the library's cards by text and filters. Returns matching cards.", tier.Read,
		func(ctx context.Context, c caller, in searchIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", "/cards/search", in.Query)
		}),
	def("get_card", "Get a card: its head revision, linked lorebooks, and its status on each target app.", tier.Read,
		func(ctx context.Context, c caller, in idIn) (*sdk.CallToolResult, error) {
			return c.both(ctx, fmt.Sprintf("/cards/%d", in.ID), fmt.Sprintf("/cards/%d/bindings", in.ID))
		}),
	def("get_lorebook", "Get a lorebook: its head revision and its status on each target app.", tier.Read,
		func(ctx context.Context, c caller, in idIn) (*sdk.CallToolResult, error) {
			return c.both(ctx, fmt.Sprintf("/lorebooks/%d", in.ID), fmt.Sprintf("/lorebooks/%d/bindings", in.ID))
		}),
	def("get_history", "List a card's or lorebook's revisions, newest first.", tier.Read,
		func(ctx context.Context, c caller, in entityIn) (*sdk.CallToolResult, error) {
			k, err := kindNamed(in.Kind)
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "GET", fmt.Sprintf("/%s/%d/revisions", k.Plural, in.ID), nil)
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
			k, err := kindNamed(in.Kind)
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "POST", fmt.Sprintf("/%s/%d/restore", k.Plural, in.ID),
				map[string]any{"revision_id": in.RevisionID, "base_revision_id": in.BaseRevisionID, "avatar": cmp.Or(in.Avatar, "keep"), "source": "mcp"})
		}),
	def("fork_card", "Make a variant of a card or lorebook: a new one with the head revision's content, image, and overrides, "+
		"and a card's collections and linked lorebooks, but not its sources or app bindings. Returns the new one.", tier.Write,
		func(ctx context.Context, c caller, in forkIn) (*sdk.CallToolResult, error) {
			k, err := kindNamed(cmp.Or(in.Kind, library.Card.Name))
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "POST", fmt.Sprintf("/%s/%d/variants", k.Plural, in.ID), map[string]any{"source": "mcp"})
		}),
	def("set_variant_of", "Record that a card or lorebook is a variant of another of the same kind, or unlink it with variant_of null. "+
		"Refuses a link that would make it its own ancestor. Changes no content.", tier.Write,
		func(ctx context.Context, c caller, in variantOfIn) (*sdk.CallToolResult, error) {
			k, err := kindNamed(cmp.Or(in.Kind, library.Card.Name))
			if err != nil {
				return nil, err
			}
			path := fmt.Sprintf("/%s/%d/variant_of", k.Plural, in.ID)
			if in.VariantOf == nil {
				return c.call(ctx, "DELETE", path, nil)
			}
			return c.call(ctx, "PUT", path, map[string]any{"id": *in.VariantOf})
		}),
	def("preview_push", "Show what pushing a card or lorebook to a target app would do: the fields it would change in the app, "+
		"with the app's value as before and the pushed one as after, fields it would drop, and lorebooks it would create. "+
		"Show the owner the changes, then pass confirm to push.", tier.Read,
		func(ctx context.Context, c caller, in previewIn) (*sdk.CallToolResult, error) {
			k, err := kindNamed(in.Kind)
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "GET", fmt.Sprintf("/%s/%d/push-preview?instance_id=%d", k.Plural, in.ID, in.InstanceID), nil)
		}),
	def("push", "Push a card or lorebook to a target app, overwriting the app's copy. Needs confirm from preview_push; "+
		"if anything changed since, it returns the new preview instead.", tier.Destructive,
		func(ctx context.Context, c caller, in pushIn) (*sdk.CallToolResult, error) {
			k, err := kindNamed(in.Kind)
			if err != nil {
				return nil, err
			}
			if in.Confirm == "" {
				return nil, fmt.Errorf("confirm is required: call preview_push and show the owner its changes first")
			}
			return c.call(ctx, "POST", fmt.Sprintf("/%s/%d/push", k.Plural, in.ID),
				map[string]any{"instance_id": in.InstanceID, "confirm": in.Confirm, "new_copy": in.NewCopy})
		}),
	def("check_drift", "Read a bound card or lorebook back from its app now, and report what changed there.", tier.Read,
		func(ctx context.Context, c caller, in bindingIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", fmt.Sprintf("/bindings/%d/check", in.BindingID), nil)
		}),
	def("list_bindings", "List the target apps a card or lorebook is pushed to, and the site sources it follows, with each one's status.", tier.Read,
		func(ctx context.Context, c caller, in entityIn) (*sdk.CallToolResult, error) {
			k, err := kindNamed(in.Kind)
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "GET", fmt.Sprintf("/%s/%d/bindings", k.Plural, in.ID), nil)
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
			k, err := kindNamed(cmp.Or(in.Kind, library.Card.Name))
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "GET", fmt.Sprintf("/instances/%d/%s", in.InstanceID, k.Plural), nil)
		}),
	def("bind", "Link a card or lorebook to one the app already has. Changes nothing in the app or the library; the binding shows ahead until the next push where they differ. The app's linked world books are left for sync.", tier.Write,
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
	def("list_jobs", "List recent jobs: imports, pushes, scans, syncs, bulk actions. A finished sync shows only its totals; get_job returns the whole snapshot.", tier.Read,
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
	def("start_import", "Import cards and lorebooks from an import plugin instance, such as a folder, or one card from its page URL "+
		"with url-import. To bring in an app's items, use start_sync.", tier.Write,
		func(ctx context.Context, c caller, in importIn) (*sdk.CallToolResult, error) {
			body := map[string]any{"instance_id": in.InstanceID}
			params := map[string]any{}
			if in.Path != "" {
				params["path"] = in.Path
			}
			if in.URL != "" {
				params["url"] = in.URL
			}
			if len(params) > 0 {
				body["params"] = params
			}
			return c.call(ctx, "POST", "/imports", body)
		}),
	def("start_sync", "Sync every target app with the library, one at a time, or one app: binds exact matches, imports new items, "+
		"and takes edits made only in an app. Pass apply \"none\" to preview. Starts a job; its finished get_job progress names "+
		"each app's reconcile job, whose progress is that app's snapshot.", tier.Write,
		func(ctx context.Context, c caller, in syncIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", "/syncs", in)
		}),
	def("refresh", "Check every app for changes and count the app items nothing is bound to. Changes nothing.", tier.Read,
		func(ctx context.Context, c caller, _ none) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", "/scans", nil)
		}),
	def("check_sources", "Check the sites cards and lorebooks came from, such as chub.ai, for updates. With id, checks that one "+
		"now and returns its bindings; without, starts a job. An update shows in list_changes with state update; review it "+
		"with compare_item and resolve_item, passing the card's or lorebook's id (and kind).", tier.Write,
		func(ctx context.Context, c caller, in sourceCheckIn) (*sdk.CallToolResult, error) {
			if in.ID == 0 {
				return c.call(ctx, "POST", "/source-checks", nil)
			}
			k, err := kindNamed(cmp.Or(in.Kind, library.Card.Name))
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "POST", fmt.Sprintf("/%s/%d/source-check", k.Plural, in.ID), nil)
		}),
	def("list_changes", "List the cards and lorebooks that need the owner, one row each with every app's or followed site source's state "+
		"(a source's is update, missing, or error), and app items no card matches first. Built from each app's latest sync, the binding "+
		"statuses, and the last source checks; counts are by instance and state. "+
		"An ahead card may have been bound to an app copy that differs, so show the owner compare_item with two_way before pushing it.", tier.Read,
		func(ctx context.Context, c caller, in changesIn) (*sdk.CallToolResult, error) {
			q := url.Values{}
			if in.InstanceID != 0 {
				q.Set("instance_id", fmt.Sprint(in.InstanceID))
			}
			if in.State != "" {
				q.Set("state", in.State)
			}
			return c.call(ctx, "GET", "/sync/changes?"+q.Encode(), nil)
		}),
	def("set_autosync", "Turn autosync on or off for a binding. With it on, each sync pushes the library's copy to that app "+
		"when the card is ahead and the app's copy hasn't changed.", tier.Destructive,
		func(ctx context.Context, c caller, in autosyncIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "PATCH", fmt.Sprintf("/bindings/%d", in.BindingID), map[string]bool{"autosync": in.Autosync})
		}),
	def("compare_item", "Compare an app item with a library card or lorebook, hunk by hunk. resolve_item needs this reply's app_hash, "+
		"head_revision_id, accepted_hash, and binding_id. An unbound item needs an id to compare with.", tier.Read,
		func(ctx context.Context, c caller, in compareIn) (*sdk.CallToolResult, error) {
			k, err := kindNamed(cmp.Or(in.Kind, library.Card.Name))
			if err != nil {
				return nil, err
			}
			path := itemPath(in.InstanceID, k, in.RemoteID)
			v := url.Values{}
			if in.ID != 0 {
				v.Set(k.Name+"_id", fmt.Sprint(in.ID))
			}
			if in.TwoWay {
				if k == library.Lorebook {
					return nil, fmt.Errorf("two_way compares cards only")
				}
				v.Set("base", "none")
			}
			path += "/compare"
			if len(v) > 0 {
				path += "?" + v.Encode()
			}
			return c.call(ctx, "GET", path, nil)
		}),
	def("resolve_item", "Bind an app item to a library card with per-hunk choices, import it, or skip it. Show the owner compare_item first. "+
		"Changes the library and the binding, never the app: push afterwards to send the result. A 409 means something changed; it has a fresh comparison.", tier.Write,
		func(ctx context.Context, c caller, in resolveIn) (*sdk.CallToolResult, error) {
			k, err := kindNamed(cmp.Or(in.Kind, library.Card.Name))
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "POST", itemPath(in.InstanceID, k, in.RemoteID)+"/resolve", in)
		}),
	def("resolve_items", "Resolve many app items as one bulk job, such as a sync's suggestions. One that fails doesn't stop the others; "+
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
