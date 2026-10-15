package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// The push and pull capabilities. A push target answers target.describe and
// target.put. One that can pull also answers target.get. A target that keeps
// lorebooks on their own also answers target.put_lorebook, and
// target.get_lorebook if it can pull. A target that can pull also answers
// target.list, and target.list_lorebooks, so the owner can bind an item the
// app already has. A target that can delete answers target.delete, and
// target.delete_lorebook if it keeps lorebooks on their own.
const (
	MethodTargetDescribe       = "target.describe"
	MethodTargetPut            = "target.put"
	MethodTargetGet            = "target.get"
	MethodTargetPutLorebook    = "target.put_lorebook"
	MethodTargetGetLorebook    = "target.get_lorebook"
	MethodTargetList           = "target.list"
	MethodTargetListLorebooks  = "target.list_lorebooks"
	MethodTargetDelete         = "target.delete"
	MethodTargetDeleteLorebook = "target.delete_lorebook"
)

// CodeRemoteNotFound is the error for an ID the plugin doesn't have: a
// remote_id the app no longer has, or a catalog item or version.
const CodeRemoteNotFound = -32004

// How a target stores lorebooks.
const (
	LorebooksEmbedded   = "embedded"
	LorebooksStandalone = "standalone"
	LorebooksNone       = "none"
)

// TargetInfo is the reply to target.describe: what the app stores.
type TargetInfo struct {
	// Fields are the CCv3 data keys the app keeps.
	Fields []string `json:"fields"`
	Pull   bool     `json:"pull"`
	// Update is false for a create-only app, where every put makes a new copy.
	Update bool `json:"update"`
	// Delete is true if target.delete removes the app's copy.
	Delete    bool   `json:"delete"`
	Lorebooks string `json:"lorebooks"`
	// LorebookFields are the character_book keys the app keeps. Required
	// when Lorebooks is standalone.
	LorebookFields []string `json:"lorebook_fields,omitempty"`
}

// Validate reports every problem with the description at once.
func (t TargetInfo) Validate() error {
	var errs []error
	if len(t.Fields) == 0 {
		errs = append(errs, errors.New("fields is empty"))
	}
	if !slices.Contains([]string{LorebooksEmbedded, LorebooksStandalone, LorebooksNone}, t.Lorebooks) {
		errs = append(errs, fmt.Errorf("lorebooks %q must be embedded, standalone, or none", t.Lorebooks))
	}
	if t.Lorebooks == LorebooksStandalone && len(t.LorebookFields) == 0 {
		errs = append(errs, errors.New("lorebook_fields is empty"))
	}
	return errors.Join(errs...)
}

// TargetPutParams sends one card. RemoteID is empty for a new card. Avatar
// is the absolute path of the avatar file in a blob store, or empty. Key is
// set for a new card: a put with a key already used, whose card the app still
// has, returns that card's remote_id instead of creating another.
type TargetPutParams struct {
	Card     json.RawMessage `json:"card"`
	RemoteID string          `json:"remote_id,omitempty"`
	Avatar   string          `json:"avatar,omitempty"`
	Key      string          `json:"key,omitempty"`
	// LorebookRemoteIDs are the card's lorebooks in the app, sorted, for a
	// target with standalone lorebooks. The app links the card to exactly these.
	LorebookRemoteIDs []string `json:"lorebook_remote_ids,omitempty"`
}

// TargetDeleteParams removes the app's copy of one card or lorebook. The
// reply is empty.
type TargetDeleteParams struct {
	RemoteID string `json:"remote_id"`
}

// TargetPutLorebookParams sends one lorebook, a CCv3 character_book.
// RemoteID and Key work as in TargetPutParams. The reply is a
// TargetPutResult.
type TargetPutLorebookParams struct {
	Lorebook json.RawMessage `json:"lorebook"`
	RemoteID string          `json:"remote_id,omitempty"`
	Key      string          `json:"key,omitempty"`
}

// TargetGetLorebookResult is the app's copy of a lorebook, for a
// target.get_lorebook with TargetGetParams.
type TargetGetLorebookResult struct {
	Lorebook json.RawMessage `json:"lorebook"`
}

// TargetPutResult names the app's copy of the card.
type TargetPutResult struct {
	RemoteID string `json:"remote_id"`
}

// TargetGetParams asks for the app's copy of one card. Avatar asks for its
// image too; only imports set it, so checks don't download every image.
type TargetGetParams struct {
	RemoteID string `json:"remote_id"`
	Avatar   bool   `json:"avatar,omitempty"`
}

// TargetGetResult is the app's copy, as a CCv3 card. Avatar is a file in
// blob_tmp, or empty. LorebookRemoteIDs are the lorebooks the app links to
// the card, for a standalone target; nil if the plugin doesn't say.
type TargetGetResult struct {
	Card              json.RawMessage `json:"card"`
	Avatar            string          `json:"avatar,omitempty"`
	LorebookRemoteIDs []string        `json:"lorebook_remote_ids,omitempty"`
}

// TargetListResult is what the app has, for target.list and
// target.list_lorebooks. Neither takes params; the plugin pages through the
// app itself.
type TargetListResult struct {
	Items []TargetItem `json:"items"`
}

// TargetItem is one card or lorebook in the app.
type TargetItem struct {
	RemoteID string `json:"remote_id"`
	Name     string `json:"name"`
}
