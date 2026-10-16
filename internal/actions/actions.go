// Package actions runs plugin actions: the action job, the checks a run must
// pass, the scope that limits what a proposal may change, and the library
// reads a plugin makes while it runs.
package actions

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/davidgibbons/innkeeper/protocol"
)

// Kind is the job kind for action runs. The job's instance is the action
// plugin's instance, and its payload is a Payload.
const Kind = "action"

// ErrInvalid means a run doesn't fit its action, such as a field the action
// doesn't offer.
var ErrInvalid = errors.New("invalid action run")

// Payload is an action job's payload: action.run's params, with context and
// images as IDs and blob SHA-256s that the job resolves when it starts.
type Payload struct {
	Action string                `json:"action"`
	Field  string                `json:"field,omitempty"`
	Entity protocol.ActionEntity `json:"entity"`
	// CardID or LorebookID is the library entity the draft belongs to, if
	// any; the field names match a push job's, so GET /jobs?card_id finds both.
	CardID      int64           `json:"card_id,omitempty"`
	LorebookID  int64           `json:"lorebook_id,omitempty"`
	Instruction string          `json:"instruction,omitempty"`
	Inputs      json.RawMessage `json:"inputs,omitempty"`
	Context     ContextRefs     `json:"context"`
	Images      ImageRefs       `json:"images"`
}

// ContextRefs is what the owner attached to a run.
type ContextRefs struct {
	Cards     []int64  `json:"cards,omitempty" maxItems:"50"`
	Lorebooks []int64  `json:"lorebooks,omitempty" maxItems:"50"`
	Skills    []string `json:"skills,omitempty" maxItems:"50"`
}

// ImageRefs are blob SHA-256s, or empty.
type ImageRefs struct {
	Reference string `json:"reference,omitempty"`
	Avatar    string `json:"avatar,omitempty"`
}

// Check reports why the run doesn't fit action a, wrapping ErrInvalid.
func (p Payload) Check(a protocol.Action) error {
	kind := p.Entity.Kind
	switch {
	case a.Applies == protocol.ApplyNew && (kind != protocol.ApplyCard || p.CardID != 0):
		return fmt.Errorf("%w: %s makes a new card; send kind card without an id", ErrInvalid, a.ID)
	case a.Applies != protocol.ApplyNew && kind != a.Applies:
		return fmt.Errorf("%w: %s applies to %s, not %q", ErrInvalid, a.ID, a.Applies, kind)
	case p.CardID != 0 && kind != protocol.ApplyCard, p.LorebookID != 0 && kind != protocol.ApplyLorebook:
		return fmt.Errorf("%w: card_id goes only with a card, and lorebook_id with a lorebook", ErrInvalid)
	case len(a.Fields) == 0 && p.Field != "":
		return fmt.Errorf("%w: %s runs on the whole %s, not a field", ErrInvalid, a.ID, kind)
	case len(a.Fields) > 0 && !slices.Contains(a.Fields, p.Field) && (p.Field != "" || !a.WholeCard):
		return fmt.Errorf("%w: %s runs on the field %v, not %q", ErrInvalid, a.ID, a.Fields, p.Field)
	case !a.Images && p.Images != ImageRefs{}:
		return fmt.Errorf("%w: %s takes no images", ErrInvalid, a.ID)
	}
	if a.Applies != protocol.ApplyNew || len(p.Entity.Data) > 0 {
		if _, err := object(p.Entity.Data); err != nil {
			return fmt.Errorf("%w: entity data must be a JSON object", ErrInvalid)
		}
	}
	return nil
}

// Progress is an action job's progress. Result is set when the run ends.
type Progress struct {
	Message string  `json:"message,omitempty"`
	Done    int64   `json:"done,omitempty"`
	Total   int64   `json:"total,omitempty"`
	Result  *Result `json:"result,omitempty"`
}

// Result is what a finished run returned, after scope enforcement.
type Result struct {
	Proposal json.RawMessage  `json:"proposal,omitempty"`
	Report   *protocol.Report `json:"report,omitempty"`
	Raw      string           `json:"raw,omitempty"`
	// Warnings name the proposal's changes the core dropped.
	Warnings []string `json:"warnings"`
}

// ListProgress is an action job's progress without its result, for job
// lists.
func ListProgress(progress json.RawMessage) json.RawMessage {
	var p Progress
	if json.Unmarshal(progress, &p) != nil {
		return progress
	}
	p.Result = nil
	out, _ := json.Marshal(p)
	return out
}
