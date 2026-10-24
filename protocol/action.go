package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The action capability changes or judges a card or lorebook. The core lists
// the actions with action.list and runs one with action.run. While a run
// lasts, the plugin may read the library with library.call.
const (
	MethodActionList  = "action.list"
	MethodActionRun   = "action.run"
	MethodLibraryCall = "library.call"
)

// What an action applies to: an item kind, or ApplyNew, a card that doesn't
// exist yet.
const (
	ApplyCard     = KindCard
	ApplyLorebook = KindLorebook
	ApplyNew      = "new"
)

var actionID = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// Action is one action a plugin offers.
type Action struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Applies     string `json:"applies" enum:"card,lorebook,new"`
	// Fields are the data keys a field action runs on. Empty for an action
	// on the whole entity.
	Fields []string `json:"fields,omitempty"`
	// WholeCard means a field action also runs on the whole entity, changing
	// any of its Fields.
	WholeCard bool `json:"whole_card,omitempty"`
	// Instruction means the action's main input is text the owner types.
	Instruction bool `json:"instruction,omitempty"`
	// Inputs is a JSON Schema object for the action's other inputs.
	Inputs     json.RawMessage `json:"inputs,omitempty"`
	ReportOnly bool            `json:"report_only,omitempty"`
	// Library means the action may send library.call.
	Library bool `json:"library,omitempty"`
	// Images means the action takes a reference image or the avatar.
	Images bool `json:"images,omitempty"`
}

// Validate checks the action's declaration.
func (a Action) Validate() error {
	var errs []error
	if !actionID.MatchString(a.ID) {
		errs = append(errs, fmt.Errorf("action id %q must be 1 to 40 lowercase letters, digits, and hyphens", a.ID))
	}
	if strings.TrimSpace(a.Label) == "" {
		errs = append(errs, fmt.Errorf("action %s: label is empty", a.ID))
	}
	if !slices.Contains([]string{ApplyCard, ApplyLorebook, ApplyNew}, a.Applies) {
		errs = append(errs, fmt.Errorf("action %s: applies %q must be card, lorebook, or new", a.ID, a.Applies))
	}
	if len(a.Fields) > 0 && a.Applies == ApplyNew {
		errs = append(errs, fmt.Errorf("action %s: fields can't go with applies new", a.ID))
	}
	if a.WholeCard && len(a.Fields) == 0 {
		errs = append(errs, fmt.Errorf("action %s: whole_card needs fields", a.ID))
	}
	if slices.Contains(a.Fields, "") {
		errs = append(errs, fmt.Errorf("action %s: a field is empty", a.ID))
	}
	errs = append(errs, unique("action "+a.ID+": field", a.Fields, nil)...)
	if len(a.Inputs) > 0 && !objectSchema(a.Inputs) {
		errs = append(errs, fmt.Errorf("action %s: inputs must be a JSON Schema with type object", a.ID))
	}
	return errors.Join(errs...)
}

// ActionListResult is the reply to action.list.
type ActionListResult struct {
	Actions []Action `json:"actions"`
	// Skills are the enabled skills a @skills/ mention can name.
	Skills []Skill `json:"skills,omitempty"`
}

// ActionEntity is the draft a run starts from. Data is absent for a new card.
type ActionEntity struct {
	Kind string          `json:"kind" enum:"card,lorebook"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ContextItem is a card or lorebook the owner attached, at its head revision.
type ContextItem struct {
	ID   int64           `json:"id"`
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

// ActionContext is what the owner attached to a run.
type ActionContext struct {
	Cards     []ContextItem `json:"cards"`
	Lorebooks []ContextItem `json:"lorebooks"`
	Skills    []string      `json:"skills"`
}

// ActionImages are absolute paths in a blob store, or empty.
type ActionImages struct {
	Reference string `json:"reference,omitempty"`
	Avatar    string `json:"avatar,omitempty"`
}

// ActionRunParams runs one action.
type ActionRunParams struct {
	Run         string          `json:"run"`
	Action      string          `json:"action"`
	Field       string          `json:"field,omitempty"`
	Entity      ActionEntity    `json:"entity"`
	Instruction string          `json:"instruction,omitempty"`
	Inputs      json.RawMessage `json:"inputs"`
	Context     ActionContext   `json:"context"`
	Images      ActionImages    `json:"images"`
}

// ActionRunResult is the reply to action.run. Proposal is the entity's
// complete new data.
type ActionRunResult struct {
	Proposal json.RawMessage `json:"proposal,omitempty"`
	Report   *Report         `json:"report,omitempty"`
	Raw      string          `json:"raw,omitempty"`
	// Values are enrichment values about the entity's data, by a key the
	// manifest's [enrichments] declares. Core stores them on a saved card
	// whose head revision holds that data, so an enrich run can skip it.
	Values map[string]json.RawMessage `json:"values,omitempty"`
	// Meta is stored with Values, as with enrich.run's.
	Meta json.RawMessage `json:"meta,omitempty"`
}

// Report is an action's judgment of an entity. Summary is Markdown.
type Report struct {
	Summary     string            `json:"summary"`
	Score       *float64          `json:"score,omitempty"`
	Dimensions  []ReportDimension `json:"dimensions,omitempty"`
	Findings    []Finding         `json:"findings,omitempty"`
	Suggestions []string          `json:"suggestions,omitempty"`
}

// ReportDimension is one scored aspect of the entity.
type ReportDimension struct {
	Key     string  `json:"key"`
	Label   string  `json:"label"`
	Score   float64 `json:"score"`
	Comment string  `json:"comment,omitempty"`
}

// Finding is one problem a report found, on the fields it names.
type Finding struct {
	Severity string       `json:"severity" enum:"high,medium,low"`
	Text     string       `json:"text"`
	Fields   []string     `json:"fields,omitempty"`
	Move     *FindingMove `json:"move,omitempty"`
}

// FindingMove suggests moving Excerpt to the field To.
type FindingMove struct {
	Excerpt string `json:"excerpt"`
	To      string `json:"to"`
}

// Validate checks what the UI needs to show the report.
func (r Report) Validate() error {
	var errs []error
	if strings.TrimSpace(r.Summary) == "" {
		errs = append(errs, errors.New("report summary is empty"))
	}
	for _, d := range r.Dimensions {
		if d.Key == "" || d.Label == "" {
			errs = append(errs, fmt.Errorf("report dimension %q needs a key and a label", d.Label))
		}
	}
	for _, f := range r.Findings {
		if !slices.Contains([]string{"high", "medium", "low"}, f.Severity) {
			errs = append(errs, fmt.Errorf("finding severity %q must be high, medium, or low", f.Severity))
		}
		if strings.TrimSpace(f.Text) == "" {
			errs = append(errs, errors.New("a finding's text is empty"))
		}
		if f.Move != nil && f.Move.To == "" {
			errs = append(errs, errors.New("a finding's move has no to field"))
		}
	}
	return errors.Join(errs...)
}

// LibraryCallParams runs one of the core's library tools for a run.
type LibraryCallParams struct {
	Run       string          `json:"run"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}
