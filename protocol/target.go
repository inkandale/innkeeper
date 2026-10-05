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
// target.get_lorebook if it can pull.
const (
	MethodTargetDescribe    = "target.describe"
	MethodTargetPut         = "target.put"
	MethodTargetGet         = "target.get"
	MethodTargetPutLorebook = "target.put_lorebook"
	MethodTargetGetLorebook = "target.get_lorebook"
)

// CodeRemoteNotFound is the error for a remote_id the app no longer has.
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
	// Delete is reserved: the core doesn't delete remote cards yet.
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

// TargetGetParams asks for the app's copy of one card.
type TargetGetParams struct {
	RemoteID string `json:"remote_id"`
}

// TargetGetResult is the app's copy, as a CCv3 card.
type TargetGetResult struct {
	Card json.RawMessage `json:"card"`
}
