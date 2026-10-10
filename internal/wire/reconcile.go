// Package wire holds API request and response types that the ink client
// shares with the server. It must not import server packages, so ink builds
// on every platform.
package wire

import "github.com/davidgibbons/innkeeper/internal/merge"

// ReconcileKind is the job kind that lines an app up against the library.
// Its final progress is the Snapshot; while it runs, {"done", "total"}.
const ReconcileKind = "target.reconcile"

// ReconcileMatch says which signals make a library card a likely match.
type ReconcileMatch struct {
	Name           *bool `json:"name" required:"false"`            // default true
	TextDistance   *int  `json:"text_distance" required:"false"`   // default 8, 0..32
	AvatarDistance *int  `json:"avatar_distance" required:"false"` // default 8, 0..32
}

// ReconcileParams are a reconcile run's settings.
type ReconcileParams struct {
	InstanceID int64          `json:"instance_id"`
	Match      ReconcileMatch `json:"match" required:"false"`
	// SplitFields is how many core fields must differ before import is
	// suggested over bind.
	SplitFields *int `json:"split_fields" required:"false"` // default 3, 1..4
	// Apply is what a run resolves itself: ApplyObvious (the default) binds
	// exact matches and imports new items; ApplyNone writes nothing.
	Apply string `json:"apply,omitempty" enum:"obvious,none" required:"false"`
}

// What a reconcile run applies; empty means ApplyObvious.
const ApplyObvious, ApplyNone = "obvious", "none"

// Snapshot is a finished reconcile run.
type Snapshot struct {
	Params    ReconcileParams `json:"params"`
	Items     []ReconcileItem `json:"items"`     // characters
	Lorebooks []ReconcileItem `json:"lorebooks"` // world books no character links to
	Missing   []int64         `json:"missing"`   // card IDs bound here whose app item is gone
	Unpushed  []int64         `json:"unpushed"`  // card IDs with no binding here
	Totals    map[string]int  `json:"totals"`    // by verdict, auto_bound, and auto_imported; each world book once
}

// ReconcileItem is one app item and how it lines up with the library.
type ReconcileItem struct {
	RemoteID   string          `json:"remote_id"`
	Name       string          `json:"name"`
	AppHash    string          `json:"app_hash"`
	Verdict    string          `json:"verdict" enum:"bound,exact,likely,new,error"`
	Error      string          `json:"error,omitempty"`
	BindingID  int64           `json:"binding_id,omitempty"` // the live binding; an import sends it back
	Candidates []Candidate     `json:"candidates,omitempty"`
	Options    []Option        `json:"options"`
	Suggested  *Option         `json:"suggested,omitempty"`
	Lorebooks  []ReconcileItem `json:"lorebooks,omitempty"`
	// AutoBound means the run bound this exact match.
	AutoBound bool `json:"auto_bound,omitempty"`
	// AutoImported means the run imported this new item.
	AutoImported bool `json:"auto_imported,omitempty"`
}

// Candidate is a library card or lorebook an app item may be a copy of.
type Candidate struct {
	CardID int64  `json:"card_id"` // or the lorebook's ID
	Name   string `json:"name"`
	// Reasons are the signals that matched: source, name, and content with
	// 0; text and avatar with their distance.
	Reasons map[string]int `json:"reasons"`
	// DifferingFields counts every field with any non-same hunk, not only the
	// core fields that decide the suggestion.
	DifferingFields int `json:"differing_fields"`
	Conflicts       int `json:"conflicts"`
	// BoundTo is the remote ID this card is already bound to on the instance.
	BoundTo string `json:"bound_to,omitempty"`
}

// Option is one way to resolve an item.
type Option struct {
	Action string `json:"action" enum:"bind,import,skip"`
	CardID int64  `json:"card_id,omitempty"`
	Why    string `json:"why,omitempty"`
}

// Comparison is one app item set field by field against one library card or
// lorebook, as a reconcile run sees the pair.
type Comparison struct {
	InstanceID int64  `json:"instance_id"`
	RemoteID   string `json:"remote_id"`
	CardID     int64  `json:"card_id"` // or the lorebook's ID
	// HeadRevisionID is the library revision compared; a resolution names it.
	HeadRevisionID int64 `json:"head_revision_id"`
	// AcceptedHash is the ancestor binding's accepted_hash, empty when Base
	// is none; a resolution names it.
	AcceptedHash string `json:"accepted_hash"`
	// Base is accepted when the pair is bound and the binding's copy is the
	// ancestor, none otherwise.
	Base string `json:"base" enum:"none,accepted"`
	// AppHash is the projected hash of the app's copy compared here.
	AppHash string `json:"app_hash"`
	// BindingID is the item's live binding, 0 when unbound; an import sends it back.
	BindingID int64         `json:"binding_id,omitempty"`
	Fields    []merge.Field `json:"fields"`
	// Avatar is set when the two avatars differ.
	Avatar *AvatarDiff `json:"avatar,omitempty"`
}

// AvatarDiff names each side's avatar blob.
type AvatarDiff struct {
	Library       string `json:"library,omitempty"` // blob sha256
	App           string `json:"app,omitempty"`
	PhashDistance *int   `json:"phash_distance,omitempty"`
}

// Resolution decides one app item: bind it to a library card, import it, or
// skip it.
type Resolution struct {
	// InstanceID and RemoteID come from the path on the single-item routes,
	// and nested world books take their parent's instance.
	InstanceID int64  `json:"instance_id" required:"false"`
	RemoteID   string `json:"remote_id" required:"false"`
	// AppHash is the comparison's app_hash; a different live copy is stale.
	AppHash string `json:"app_hash" required:"false"`
	// BaseRevisionID is the comparison's head_revision_id; a moved head is stale.
	BaseRevisionID int64 `json:"base_revision_id" required:"false"`
	// AcceptedHash is the comparison's accepted_hash, empty for an item that
	// wasn't bound to the card; a binding that changed since is stale.
	AcceptedHash string `json:"accepted_hash" required:"false"`
	// BindingID is the item's live binding as the comparison or reconcile saw
	// it; 0 means it was unbound. An import whose binding changed since is stale.
	BindingID int64  `json:"binding_id" required:"false"`
	Action    string `json:"action" enum:"bind,import,skip"`
	// CardID is any library card or lorebook for bind; 0 means the bound one.
	CardID int64 `json:"card_id,omitempty"`
	// Choices maps hunk IDs, and avatar, to a choice.
	Choices map[string]merge.Choice `json:"choices,omitempty"`
	// Default is the choice for hunks not in Choices; empty means library.
	Default   merge.Choice `json:"default,omitempty"`
	Lorebooks []Resolution `json:"lorebooks,omitempty"`
	// Kind says whether a top-level item in a bulk resolve is a card or a
	// world book; empty means card. Nested items are world books.
	Kind string `json:"kind,omitempty" enum:"card,lorebook"`
}
