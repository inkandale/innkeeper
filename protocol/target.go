package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// The push and pull capabilities. A push target answers target.describe and
// target.put. One that can pull also answers target.get. The names
// target.put_lorebook and target.get_lorebook are reserved for targets that
// keep lorebooks on their own.
const (
	MethodTargetDescribe = "target.describe"
	MethodTargetPut      = "target.put"
	MethodTargetGet      = "target.get"
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
