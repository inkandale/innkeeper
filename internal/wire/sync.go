package wire

import (
	"encoding/json"
	"time"
)

// Change is one top-level field the app changed.
type Change struct {
	Field string `json:"field"`
	// Before is absent if the field was missing at the last push or resolve.
	Before json.RawMessage `json:"before,omitempty"`
	// After is absent if the field is missing from the app.
	After json.RawMessage `json:"after,omitempty"`
}

// Push is one push of a card or lorebook to a target, or the bind that
// linked it to an item the app already had.
type Push struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind" enum:"push,bind"`
	RevisionID int64  `json:"revision_id"`
	// LorebookRevisionIDs are the embedded lorebooks' revisions; absent when
	// the target doesn't embed lorebooks.
	LorebookRevisionIDs []int64 `json:"lorebook_revision_ids,omitempty"`
	// LorebookRemoteIDs are the card's lorebooks in the app; absent unless
	// the target keeps lorebooks on their own.
	LorebookRemoteIDs []string  `json:"lorebook_remote_ids,omitempty"`
	PushedHash        string    `json:"pushed_hash"`
	RemoteID          string    `json:"remote_id"`
	PushedAt          time.Time `json:"pushed_at"`
}

// Binding is a card's or lorebook's link to its copy in one target app.
// Exactly one of CardID and LorebookID is set.
type Binding struct {
	ID         int64  `json:"id"`
	InstanceID int64  `json:"instance_id"`
	CardID     int64  `json:"card_id,omitempty"`
	LorebookID int64  `json:"lorebook_id,omitempty"`
	RemoteID   string `json:"remote_id"`
	Status     string `json:"status" enum:"in_sync,ahead,remote_changed,diverged,missing,cant_check"`
	// Ahead means the library changed since the last push.
	Ahead       bool   `json:"ahead"`
	RemoteState string `json:"remote_state" enum:"unknown,unchanged,changed,missing"`
	// Autosync means each sync pushes the library's copy when the card is
	// ahead and the app's copy hasn't changed.
	Autosync bool `json:"autosync"`
	// Push is false for a binding that follows a site source: the core only
	// reads it, and it is never ahead.
	Push bool `json:"push"`
	// Paused means checks skip the binding, as when the owner ignores a source's changes.
	Paused bool `json:"paused"`
	// Imported marks the binding an import keeps for the item it came from;
	// its updates arrive with the next import instead of a check.
	Imported bool `json:"imported"`
	// CheckFailures counts a followed source's checks that failed in a row;
	// CheckError is the last one's error.
	CheckFailures int        `json:"check_failures"`
	CheckError    string     `json:"check_error,omitempty"`
	CheckedAt     *time.Time `json:"checked_at,omitempty"`
	LastPush      Push       `json:"last_push"`
	// Changes are the fields the app changed since the last push or resolve.
	Changes []Change `json:"changes,omitempty"`
	// AppHash is the app's copy as the last check saw it, when it differed.
	// Dismiss takes it, so a change made since is refused rather than ignored unseen.
	AppHash   string    `json:"app_hash,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Preview is what a push would send, for the owner to confirm first.
type Preview struct {
	// Card or Lorebook is what the target would get.
	Card     json.RawMessage `json:"card,omitempty"`
	Lorebook json.RawMessage `json:"lorebook,omitempty"`
	// DroppedFields are fields with content that the target doesn't store.
	DroppedFields []string `json:"dropped_fields"`
	// NewCopy means the push makes another copy, because the target can't
	// update the one it has.
	NewCopy bool `json:"new_copy"`
	// Replaces means the push overwrites the app's copy. Changes lists what
	// it changes there: Before is the app's value, After the pushed one.
	Replaces bool     `json:"replaces"`
	Changes  []Change `json:"changes"`
	// Stale means the app couldn't be read, so Changes compare against what
	// the last push or resolve accepted.
	Stale bool `json:"stale,omitempty"`
	// CreateLorebooks are the card's lorebooks that the push creates in the
	// app first, for a target that keeps lorebooks on their own.
	CreateLorebooks []int64 `json:"create_lorebooks,omitempty"`
	// Confirm identifies this preview. A push that sends it goes ahead only
	// if a new preview would be the same, so nothing changes unseen.
	Confirm string `json:"confirm"`
}

// BulkPreview counts how the cards a query push would send stand on the
// target, as of each binding's last check.
type BulkPreview struct {
	Total int `json:"total"`
	// New are unbound, or missing from the app; the push creates them.
	New       int `json:"new"`
	Ahead     int `json:"ahead"`
	InSync    int `json:"in_sync"`
	CantCheck int `json:"cant_check"`
	// Drifted were changed in the app; the push overwrites those changes.
	Drifted int `json:"drifted"`
	// Confirm identifies this preview. A push that sends it is refused if a
	// new preview would differ, and replaces only the app copies it counted.
	Confirm string `json:"confirm"`
}
