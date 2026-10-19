package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The records capability keeps data the owner manages on the instance's
// settings page. The plugin stores it; the core only passes calls along.
const (
	MethodRecordsList   = "records.list"
	MethodRecordsGet    = "records.get"
	MethodRecordsPut    = "records.put"
	MethodRecordsDelete = "records.delete"
	MethodRecordsImport = "records.import"
)

// RecordType is one [records.<type>] table in the manifest.
type RecordType struct {
	Label string `toml:"label"`
	// Schema is a JSON Schema object for one record.
	Schema map[string]any `toml:"schema"`
	// Import is what Import offers: file, url, or both.
	Import []string `toml:"import"`
}

func (it RecordType) validate(key string) []error {
	var errs []error
	if !keyPattern.MatchString(key) {
		errs = append(errs, fmt.Errorf("record type %q must match %s", key, keyPattern))
	}
	if strings.TrimSpace(it.Label) == "" {
		errs = append(errs, fmt.Errorf("record type %q: label is required", key))
	}
	if raw, err := json.Marshal(it.Schema); err != nil || !objectSchema(raw) {
		errs = append(errs, fmt.Errorf("record type %q: schema must be a JSON Schema with type object", key))
	}
	return append(errs, unique(fmt.Sprintf("record type %q: import", key), it.Import, oneOf(importInputs))...)
}

// RecordSummary is one record in a list.
type RecordSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Validate checks what the UI needs to list and address the record.
func (s RecordSummary) Validate() error {
	errs := []error{CheckRecordID(s.ID)}
	if strings.TrimSpace(s.Name) == "" {
		errs = append(errs, fmt.Errorf("record %s: name is empty", s.ID))
	}
	return errors.Join(errs...)
}

// CheckRecordID reports whether id can name a record in a URL path, and
// can't climb out of a folder if a plugin uses it as a file name.
func CheckRecordID(id string) error {
	if id == "" || id == "." || id == ".." || len(id) > 256 || strings.Contains(id, "/") {
		return fmt.Errorf("record id %q must be 1 to 256 bytes without /, and not . or ..", id)
	}
	return nil
}

// RecordsListParams lists one record type.
type RecordsListParams struct {
	Type string `json:"type"`
}

// RecordsListResult is the reply to records.list.
type RecordsListResult struct {
	Records []RecordSummary `json:"records"`
}

// RecordRef names one record, for records.get and records.delete.
type RecordRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// RecordsGetResult is the reply to records.get.
type RecordsGetResult struct {
	Record json.RawMessage `json:"record"`
}

// RecordsPutParams creates a record, or replaces the one ID names.
type RecordsPutParams struct {
	Type   string          `json:"type"`
	ID     string          `json:"id,omitempty"`
	Record json.RawMessage `json:"record"`
}

// RecordsPutResult is the record's ID, which a replace may change.
type RecordsPutResult struct {
	ID string `json:"id"`
}

// RecordsImportParams imports from a URL, or from a file in blob_tmp at Path
// that the owner uploaded as Name.
type RecordsImportParams struct {
	Type string `json:"type"`
	URL  string `json:"url,omitempty"`
	Path string `json:"path,omitempty"`
	Name string `json:"name,omitempty"`
}

// RecordsImportResult lists what an import added or replaced.
type RecordsImportResult struct {
	Records []RecordSummary `json:"records"`
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
