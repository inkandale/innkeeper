package protocol

import (
	"encoding/json"
	"time"
)

// Version is the protocol version this core speaks.
const Version = 1

// Methods every plugin implements.
const (
	MethodInitialize = "initialize"
	MethodShutdown   = "shutdown"
)

// InitializeParams is sent by the core as the first request to a plugin.
type InitializeParams struct {
	Protocol    int               `json:"protocol"`
	InstanceID  string            `json:"instance_id"`
	Config      map[string]any    `json:"config"`
	Secrets     map[string]string `json:"secrets"`
	BlobStores  map[string]string `json:"blob_stores"`
	BlobTmp     string            `json:"blob_tmp"`
	DatabaseURL string            `json:"database_url"`
}

// InitializeResult is the plugin's reply to initialize.
type InitializeResult struct {
	Protocol     int      `json:"protocol"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
	// Services are the service methods this instance offers, for ai.
	Services []string `json:"services,omitempty"`
}

// The import capability. The core calls import.run; until it replies, the
// plugin sends the notifications below, each carrying the call's Run token.
const (
	MethodImportRun    = "import.run"
	NotifyEmitCard     = "emit.card"
	NotifyEmitLorebook = "emit.lorebook"
	NotifyProgress     = "progress"
	NotifyCheckpoint   = "checkpoint"
	NotifyLog          = "log"
)

// ImportRunParams starts an import. Checkpoint is null on the first run, or
// the last checkpoint the plugin sent before an interrupted run.
type ImportRunParams struct {
	Run        string          `json:"run"`
	Params     json.RawMessage `json:"params"`
	Checkpoint json.RawMessage `json:"checkpoint"`
}

// EmitCard sends one imported card. Item is the plugin's stable ID for the
// source, Card is a CCv3 card, and Avatar is a file in blob_tmp, or empty.
type EmitCard struct {
	Run    string          `json:"run"`
	Item   string          `json:"item"`
	Card   json.RawMessage `json:"card"`
	Avatar string          `json:"avatar,omitempty"`
	// Version is the source's label for this version, the revision's message.
	Version string `json:"version,omitempty"`
	// Created is the source's date for this version, the revision's created_at.
	Created time.Time `json:"created,omitzero"`
	// Values are values for keys the manifest's [enrichments] declares,
	// stored as enrichments from the importing instance.
	Values map[string]json.RawMessage `json:"values,omitempty"`
}

// EmitLorebook sends one standalone lorebook, a CCv3 character_book.
type EmitLorebook struct {
	Run      string          `json:"run"`
	Item     string          `json:"item"`
	Lorebook json.RawMessage `json:"lorebook"`
}

// Progress reports how far the import has got. Total is 0 when unknown.
type Progress struct {
	Run     string `json:"run"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total,omitempty"`
	Message string `json:"message,omitempty"`
}

// Log adds a line to the log of the job a run belongs to, where the owner
// reads it on the job's page. Any run may send it. Level is info, warn, or
// error; empty means info.
type Log struct {
	Run     string `json:"run"`
	Level   string `json:"level,omitempty"`
	Message string `json:"message"`
}

// Checkpoint is where to resume if the run is interrupted. The core saves it
// after handling every notification sent before it.
type Checkpoint struct {
	Run        string          `json:"run"`
	Checkpoint json.RawMessage `json:"checkpoint"`
}

// The auth capability. A login calls auth.begin, then auth.complete with
// what the user sent back.
const (
	MethodAuthBegin    = "auth.begin"
	MethodAuthComplete = "auth.complete"
)

// AuthBeginParams starts a login. CallbackURL is where a redirect login must
// send the browser back to, or empty if the core has no public URL.
type AuthBeginParams struct {
	CallbackURL string `json:"callback_url"`
}

// AuthBeginResult says how to log in: send the browser to Redirect, or show
// Form, a JSON Schema of the fields to ask for. State comes back unchanged
// in auth.complete.
type AuthBeginResult struct {
	Redirect string          `json:"redirect,omitempty"`
	Form     json.RawMessage `json:"form,omitempty"`
	State    string          `json:"state,omitempty"`
}

// AuthCompleteParams carries what the user sent back: the form's fields, or
// the query parameters of the redirect to CallbackURL.
type AuthCompleteParams struct {
	Params      map[string]string `json:"params"`
	State       string            `json:"state"`
	CallbackURL string            `json:"callback_url"`
}

// AuthCompleteResult is the verified identity. Subject must be stable for
// the same person on the same instance.
type AuthCompleteResult struct {
	Subject string `json:"subject"`
	Name    string `json:"name,omitempty"`
}

// Optional auth methods that let the owner claim a new server. auth.setup
// returns a form; auth.setup.complete turns its fields into the secrets of
// the instance the core creates. Both run before any instance exists, with
// an empty config and no secrets.
const (
	MethodAuthSetup         = "auth.setup"
	MethodAuthSetupComplete = "auth.setup.complete"
)

// AuthSetupResult is the setup form, a JSON Schema object like
// AuthBeginResult.Form.
type AuthSetupResult struct {
	Form json.RawMessage `json:"form"`
}

// AuthSetupCompleteParams carries the setup form's fields.
type AuthSetupCompleteParams struct {
	Params map[string]string `json:"params"`
}

// AuthSetupCompleteResult is the new instance's secrets and the subject
// auth.complete will report for the owner.
type AuthSetupCompleteResult struct {
	Secrets map[string]string `json:"secrets"`
	Subject string            `json:"subject"`
}
