package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The items capability keeps data the owner manages on the instance's
// settings page. The plugin stores it; the core only passes calls along.
const (
	MethodItemsList   = "items.list"
	MethodItemsGet    = "items.get"
	MethodItemsPut    = "items.put"
	MethodItemsDelete = "items.delete"
	MethodItemsImport = "items.import"
)

// ItemType is one [items.<type>] table in the manifest.
type ItemType struct {
	Label string `toml:"label"`
	// Schema is a JSON Schema object for one item.
	Schema map[string]any `toml:"schema"`
	// Import is what Import offers: file, url, or both.
	Import []string `toml:"import"`
}

func (it ItemType) validate(key string) []error {
	var errs []error
	if !keyPattern.MatchString(key) {
		errs = append(errs, fmt.Errorf("item type %q must match %s", key, keyPattern))
	}
	if strings.TrimSpace(it.Label) == "" {
		errs = append(errs, fmt.Errorf("item type %q: label is required", key))
	}
	if raw, err := json.Marshal(it.Schema); err != nil || !objectSchema(raw) {
		errs = append(errs, fmt.Errorf("item type %q: schema must be a JSON Schema with type object", key))
	}
	for i, in := range it.Import {
		if !slices.Contains(importInputs, in) {
			errs = append(errs, fmt.Errorf("item type %q: import %q must be one of %v", key, in, importInputs))
		}
		if slices.Contains(it.Import[:i], in) {
			errs = append(errs, fmt.Errorf("item type %q: import %q listed twice", key, in))
		}
	}
	return errs
}

// ItemSummary is one item in a list.
type ItemSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Validate checks what the UI needs to list and address the item.
func (s ItemSummary) Validate() error {
	var errs []error
	if s.ID == "" || len(s.ID) > 256 || strings.Contains(s.ID, "/") {
		errs = append(errs, fmt.Errorf("item id %q must be 1 to 256 bytes without /", s.ID))
	}
	if strings.TrimSpace(s.Name) == "" {
		errs = append(errs, fmt.Errorf("item %s: name is empty", s.ID))
	}
	return errors.Join(errs...)
}

// ItemsListParams lists one item type.
type ItemsListParams struct {
	Type string `json:"type"`
}

// ItemsListResult is the reply to items.list.
type ItemsListResult struct {
	Items []ItemSummary `json:"items"`
}

// ItemRef names one item, for items.get and items.delete.
type ItemRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// ItemsGetResult is the reply to items.get.
type ItemsGetResult struct {
	Item json.RawMessage `json:"item"`
}

// ItemsPutParams creates an item, or replaces the one ID names.
type ItemsPutParams struct {
	Type string          `json:"type"`
	ID   string          `json:"id,omitempty"`
	Item json.RawMessage `json:"item"`
}

// ItemsPutResult is the item's ID, which a replace may change.
type ItemsPutResult struct {
	ID string `json:"id"`
}

// ItemsImportParams imports from a URL, or from a file in blob_tmp at Path
// that the owner uploaded as Name.
type ItemsImportParams struct {
	Type string `json:"type"`
	URL  string `json:"url,omitempty"`
	Path string `json:"path,omitempty"`
	Name string `json:"name,omitempty"`
}

// ItemsImportResult lists what an import added or replaced.
type ItemsImportResult struct {
	Items []ItemSummary `json:"items"`
}

var skillName = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// Skill is one skill an action plugin can force on for a run.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Validate checks that a @skills/ mention can name the skill.
func (s Skill) Validate() error {
	if !skillName.MatchString(s.Name) {
		return fmt.Errorf("skill name %q must be 1 to 64 lowercase letters, digits, and hyphens", s.Name)
	}
	return nil
}
