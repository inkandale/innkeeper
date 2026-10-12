package wire

// App states in the changes queue.
const (
	ChangeDiffers     = "differs"
	ChangeBothChanged = "both_changed"
	ChangeAhead       = "ahead"
	ChangeMissing     = "missing"
	ChangeError       = "error"
	ChangeNew         = "new"
)

// AppChange is one app's change to a card, or an app item no card matches.
type AppChange struct {
	InstanceID int64  `json:"instance_id"`
	State      string `json:"state" enum:"differs,both_changed,ahead,missing,error,new"`
	RemoteID   string `json:"remote_id,omitempty"`
	// Name is the app item's name.
	Name      string `json:"name,omitempty"`
	BindingID int64  `json:"binding_id,omitempty"`
	// JobID is the reconcile job whose snapshot the state came from; 0 for ahead and missing.
	JobID     int64   `json:"job_id,omitempty"`
	AppHash   string  `json:"app_hash,omitempty"`
	Suggested *Option `json:"suggested,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// ChangeRow is a library card, or an app item no card matches, with its changes.
type ChangeRow struct {
	// CardID is 0 for an app item no card matches.
	CardID int64       `json:"card_id,omitempty"`
	Name   string      `json:"name"`
	Apps   []AppChange `json:"apps"`
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
