package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// The ui capability shows a plugin's web bundle in a sandboxed panel. The
// panel reaches its plugin through ui.call, which replies when the call ends
// and may send ui.event notifications before that.
const (
	MethodUICall  = "ui.call"
	NotifyUIEvent = "ui.event"
)

// MaxUIEvent is the largest ui.event data core forwards.
const MaxUIEvent = 64 << 10

// SlotCorner is a chat-sized panel opened from a bubble in the page's corner.
const SlotCorner = "corner"

// What the owner can grant an instance. Each is off until granted.
const (
	PermissionLibraryRead  = "library.read"
	PermissionCardsPush    = "cards.push"
	PermissionCardsCollect = "cards.collect"
	// PermissionCardsEdit shows the panel the open Forge's draft and lets it send changes to Review.
	PermissionCardsEdit = "cards.edit"
	// PermissionPanel shows the panel's bubble. Core offers it on every ui
	// plugin, so a manifest doesn't list it.
	PermissionPanel = "panel"
)

var uiPermissions = []string{PermissionLibraryRead, PermissionCardsPush, PermissionCardsCollect, PermissionCardsEdit}

// UI is a plugin's [ui] table.
type UI struct {
	// Title names the panel on its bubble, its header, and its Show switch.
	Title string `toml:"title"`
	// Entry is the bundle's HTML file. Core serves the folder holding it to
	// anyone without a login, so the folder must hold nothing else.
	Entry       string   `toml:"entry"`
	Slot        string   `toml:"slot"`
	Permissions []string `toml:"permissions"`
}

// UICallParams is one call from a plugin's panel.
type UICallParams struct {
	Run    string          `json:"run"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// UIEvent is one piece of data for the panel that made the call.
type UIEvent struct {
	Run  string          `json:"run"`
	Data json.RawMessage `json:"data"`
}

func (m *Manifest) validateUI() []error {
	if m.UI == nil {
		if m.Has(CapabilityUI) {
			return []error{errors.New("the ui capability needs a [ui] table")}
		}
		return nil
	}
	var errs []error
	if !m.Has(CapabilityUI) {
		errs = append(errs, errors.New("ui needs the ui capability"))
	}
	if strings.TrimSpace(m.UI.Title) == "" {
		errs = append(errs, errors.New("ui needs a title"))
	}
	switch {
	case m.UI.Entry == "":
		errs = append(errs, errors.New("ui needs an entry"))
	case !filepath.IsLocal(m.UI.Entry):
		errs = append(errs, fmt.Errorf("entry %q is outside the plugin folder", m.UI.Entry))
	case filepath.Dir(m.UI.Entry) == ".":
		errs = append(errs, fmt.Errorf("entry %q must be in a subfolder, since that whole folder is served", m.UI.Entry))
	}
	if m.UI.Slot != SlotCorner {
		errs = append(errs, fmt.Errorf("slot %q must be %s", m.UI.Slot, SlotCorner))
	}
	for _, p := range m.UI.Permissions {
		if !slices.Contains(uiPermissions, p) {
			errs = append(errs, fmt.Errorf("permission %q must be one of %v", p, uiPermissions))
		}
	}
	return append(errs, unique("permission", m.UI.Permissions, nil)...)
}

// Permissions is what the owner can grant the plugin's instances: panel
// first for a ui plugin, since it decides whether the panel shows at all.
// An action plugin can always ask to read the library.
func (m *Manifest) Permissions() []string {
	var out []string
	if m.UI != nil {
		out = append(out, PermissionPanel)
	}
	if m.Has(CapabilityAction) {
		out = append(out, PermissionLibraryRead)
	}
	if m.UI != nil {
		for _, p := range m.UI.Permissions {
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}
