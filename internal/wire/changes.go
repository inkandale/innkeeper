package wire

// App states in the changes queue.
const (
	ChangeDiffers     = "differs"
	ChangeBothChanged = "both_changed"
	ChangeAhead       = "ahead"
	ChangeMissing     = "missing"
	ChangeError       = "error"
	ChangeNew         = "new"
	// ChangeUpdate is a followed site source whose author changed it.
	ChangeUpdate = "update"
)

// AppChange is an app's or source's change, or an app item no card matches.
type AppChange struct {
	InstanceID int64  `json:"instance_id"`
	State      string `json:"state" enum:"differs,both_changed,ahead,missing,error,new,update"`
	RemoteID   string `json:"remote_id,omitempty"`
	// Name is the app item's name.
	Name      string `json:"name,omitempty"`
	BindingID int64  `json:"binding_id,omitempty"`
	// Push is true for an app's entry and false for a followed site source's,
	// whose actions differ: it can't be pushed again or unbound.
	Push bool `json:"push"`
	// SourceID is a followed source's card_source or lorebook_source ID, for removing it.
	SourceID int64 `json:"source_id,omitempty"`
	// JobID is the reconcile job whose snapshot the state came from; 0 for ahead and missing.
	JobID     int64          `json:"job_id,omitempty"`
	AppHash   string         `json:"app_hash,omitempty"`
	Suggested *ResolveOption `json:"suggested,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// ChangeRow is a card, a lorebook, or an unmatched app item, with its changes.
type ChangeRow struct {
	// Kind says whether the row is a card or a lorebook, which an app item no
	// library entry matches also needs.
	Kind string `json:"kind" enum:"card,lorebook"`
	// CardID is the card; 0 for a lorebook or an app item nothing matches.
	CardID int64 `json:"card_id,omitempty"`
	// LorebookID is set instead of CardID for a lorebook.
	LorebookID int64       `json:"lorebook_id,omitempty"`
	Name       string      `json:"name"`
	Apps       []AppChange `json:"apps"`
}

// Changes is the changes queue.
type Changes struct {
	Rows []ChangeRow `json:"rows"`
	// Counts are by instance ID, then state, before any filter.
	Counts map[int64]map[string]int `json:"counts"`
}

// ChangesFilter narrows the rows; zero values match everything.
type ChangesFilter struct {
	InstanceID int64
	State      string
}
