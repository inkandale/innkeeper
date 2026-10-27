package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// The capabilities a plugin may declare. They are untyped so they pass
// straight into the []string a manifest decodes and a handshake reports.
const (
	CapabilityImport   = "import"
	CapabilityPush     = "push"
	CapabilityPull     = "pull"
	CapabilityAction   = "action"
	CapabilityRecords  = "records"
	CapabilityEnrich   = "enrich"
	CapabilityCodec    = "codec"
	CapabilityAuth     = "auth"
	CapabilityMCP      = "mcp"
	CapabilityTheme    = "theme"
	CapabilityCatalog  = "catalog"
	CapabilitySites    = "sites"
	CapabilityAI       = "ai"
	CapabilitySettings = "settings"
)

// Capabilities lists every capability a plugin may declare.
var Capabilities = []string{CapabilityImport, CapabilityPush, CapabilityPull, CapabilityAction, CapabilityRecords,
	CapabilityEnrich, CapabilityCodec, CapabilityAuth, CapabilityMCP, CapabilityTheme, CapabilityCatalog,
	CapabilitySites, CapabilityAI, CapabilitySettings}

// WithoutRetired drops capabilities an older core defined, so a manifest or
// handshake that still lists one keeps working.
// ponytail: drop this and its callers a release after search was removed.
func WithoutRetired(capabilities []string) []string {
	return slices.DeleteFunc(slices.Clone(capabilities), func(c string) bool { return c == "search" })
}

var enrichmentTypes = []string{"text", "score", "list"}

// importInputs are the import params the UI can send: file sends blob and name, url sends url,
// and folder sends none, importing the folder the instance's config names.
var importInputs = []string{"file", "url", "folder"}

// Names become PostgreSQL schema and role names (plugin_<name>, with - as
// _). 41 characters makes that 48 bytes, leaving headroom under Postgres's
// 63-byte limit for suffixes.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

const maxNameLen = 41

// Secret names and enrichment keys.
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Codec formats, such as png or json-v2.
var formatPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Manifest is a plugin's plugin.toml.
type Manifest struct {
	Name         string                `toml:"name"`
	Version      string                `toml:"version"`
	Protocol     int                   `toml:"protocol"`
	Command      []string              `toml:"command"`
	Capabilities []string              `toml:"capabilities"`
	Secrets      []string              `toml:"secrets"`
	Config       map[string]any        `toml:"config"`
	Enrichments  map[string]Enrichment `toml:"enrichments"`

	// Formats are what a codec plugin's codec.encode writes.
	Formats []string `toml:"formats"`

	// Inputs are what an import plugin takes from the UI.
	Inputs []string `toml:"inputs"`

	// Bulk makes the core store an import's cards a checkpoint at a time, in
	// one transaction, without queuing source detection.
	Bulk bool `toml:"bulk"`

	// EnrichMaxCards is the most cards one enrich run of the plugin takes,
	// or 0 for the core's default. A plugin whose calls cost nothing can raise it.
	EnrichMaxCards int `toml:"enrich_max_cards"`

	// SourceKinds are the site source kinds a pull-only plugin follows: the
	// core binds each such source to its instance as remote_id kind:ref.
	SourceKinds []string `toml:"source_kinds"`

	// Records are the record types a records plugin keeps, by type name.
	Records map[string]RecordType `toml:"records"`

	// Settings holds the buttons a settings plugin adds to its instance form.
	Settings Settings `toml:"settings"`

	// Database gives the plugin a schema, plugin_<name>, and a role limited
	// to it. The URL arrives as initialize's database_url.
	Database bool `toml:"database"`

	// MultipleInstances lets the owner create more than one instance;
	// without it, a plugin has at most one.
	MultipleInstances bool `toml:"multiple_instances"`

	// Title is the name the UI shows, such as "SillyTavern".
	Title string `toml:"title"`

	// Description is one line saying what the plugin does.
	Description string `toml:"description"`

	// Category groups the plugin on the Plugins page, such as "Apps".
	Category string `toml:"category"`

	// Uses are the service methods the plugin calls on other instances.
	Uses []string `toml:"uses"`

	// Stylesheet is a theme plugin's CSS file, inside the plugin folder.
	Stylesheet string `toml:"stylesheet"`

	// Icon is the app's logo, inside the plugin folder; its extension is
	// one of IconTypes.
	Icon string `toml:"icon"`

	// Dir is the absolute path of the plugin's folder.
	Dir string `toml:"-"`
}

// DisplayName is the plugin's Title, or its Name without one.
func (m *Manifest) DisplayName() string {
	if m.Title != "" {
		return m.Title
	}
	return m.Name
}

// Enrichment declares one enrichment key a plugin writes and how the UI
// shows it.
type Enrichment struct {
	Type  string    `toml:"type"`
	Label string    `toml:"label"`
	Range []float64 `toml:"range"`
	// Grid shows the key on the library grid. At most one key sets it.
	Grid bool `toml:"grid"`
	// Priority orders grid keys across plugins: a card's tile shows the
	// highest-priority value it has. Space values apart, such as by 100, so
	// later plugins fit between.
	Priority int `toml:"priority"`
}

// LoadManifest reads and validates dir/plugin.toml. Unknown keys are errors,
// so typos fail loudly.
func LoadManifest(dir string) (*Manifest, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	var m Manifest
	md, err := toml.DecodeFile(filepath.Join(abs, "plugin.toml"), &m)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var unknown []string
	for _, k := range md.Undecoded() {
		// The decoder lists nested tables in a map[string]any as undecoded
		// though they are in the map: config and each record type's schema.
		if k[0] != "config" && !(len(k) > 2 && k[0] == "records" && k[2] == "schema") {
			unknown = append(unknown, k.String())
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("manifest %s: unknown keys: %s", abs, strings.Join(unknown, ", "))
	}
	orderProperties(m.Config, md.Keys())
	m.Dir = abs
	m.Capabilities = WithoutRetired(m.Capabilities)
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("manifest %s: %w", abs, err)
	}
	if m.IsTheme() {
		if err := checkFile(m.Dir, m.Stylesheet); err != nil {
			return nil, fmt.Errorf("manifest %s: stylesheet: %w", abs, err)
		}
	}
	if m.Icon != "" {
		if err := checkFile(m.Dir, m.Icon); err != nil {
			return nil, fmt.Errorf("manifest %s: icon: %w", abs, err)
		}
	}
	return &m, nil
}

// orderProperties sets x-order on each object in the config schema to its
// properties in the order the manifest lists them, which a map loses.
func orderProperties(config map[string]any, keys []toml.Key) {
	for _, k := range keys {
		n := len(k)
		if n < 3 || k[0] != "config" || k[n-2] != "properties" {
			continue
		}
		parent := config
		for _, step := range k[1 : n-2] {
			parent, _ = parent[step].(map[string]any)
		}
		if parent == nil {
			continue
		}
		order, _ := parent["x-order"].([]any)
		if !slices.Contains(order, any(k[n-1])) {
			parent["x-order"] = append(order, k[n-1])
		}
	}
}

// checkFile reports whether rel is a regular file in dir. OpenInRoot
// refuses a symlink that leaves the folder.
func checkFile(dir, rel string) error {
	f, err := os.OpenInRoot(dir, rel)
	if err != nil {
		return err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", rel)
	}
	return nil
}

// IconTypes maps each icon file extension a manifest may use to its
// content type.
var IconTypes = map[string]string{".svg": "image/svg+xml", ".png": "image/png", ".webp": "image/webp"}

// IconType is the icon's content type, or "" when there's no icon.
func (m *Manifest) IconType() string { return IconTypes[filepath.Ext(m.Icon)] }

// Validate reports every problem with the manifest at once.
func (m *Manifest) Validate() error {
	var errs []error
	if !namePattern.MatchString(m.Name) || len(m.Name) > maxNameLen {
		errs = append(errs, fmt.Errorf("name %q must match %s and be at most %d characters", m.Name, namePattern, maxNameLen))
	}
	if m.Version == "" {
		errs = append(errs, errors.New("version is required"))
	}
	if m.Protocol != Version {
		errs = append(errs, fmt.Errorf("protocol %d is not supported; this core speaks %d", m.Protocol, Version))
	}
	if m.IsTheme() {
		errs = append(errs, m.validateTheme()...)
	} else if len(m.Command) == 0 || m.Command[0] == "" {
		errs = append(errs, errors.New("command is required"))
	} else if m.Dir != "" && !filepath.IsAbs(m.Command[0]) && strings.Contains(m.Command[0], "/") {
		if !inside(m.Dir, m.CommandPath()) {
			errs = append(errs, fmt.Errorf("command %q is outside the plugin folder", m.Command[0]))
		}
	}
	switch {
	case m.Icon == "":
	case m.IconType() == "":
		errs = append(errs, fmt.Errorf("icon %q must end in one of %v", m.Icon, slices.Sorted(maps.Keys(IconTypes))))
	case filepath.IsAbs(m.Icon) || (m.Dir != "" && !inside(m.Dir, filepath.Join(m.Dir, m.Icon))):
		errs = append(errs, fmt.Errorf("icon %q is outside the plugin folder", m.Icon))
	}
	if len(m.Config) > 0 {
		if raw, err := json.Marshal(m.Config); err != nil || !objectSchema(raw) {
			errs = append(errs, errors.New("config must be a JSON Schema with type object"))
		}
		described, _ := m.Config["x-secrets"].(map[string]any)
		for _, name := range slices.Sorted(maps.Keys(described)) {
			if !slices.Contains(m.Secrets, name) {
				errs = append(errs, fmt.Errorf("config x-secrets %q is not in secrets", name))
			}
		}
	}
	if strings.ContainsAny(m.Description, "\r\n") {
		errs = append(errs, errors.New("description must be one line"))
	}
	if len(m.Capabilities) == 0 {
		errs = append(errs, errors.New("at least one capability is required"))
	}
	for _, c := range m.Capabilities {
		if !slices.Contains(Capabilities, c) {
			errs = append(errs, fmt.Errorf("unknown capability %q", c))
		}
	}
	errs = append(errs, unique("capability", m.Capabilities, nil)...)
	errs = append(errs, m.checkCapabilityKeys()...)
	errs = append(errs, unique("secret name", m.Secrets, matches(keyPattern))...)
	for key, e := range m.Enrichments {
		if !keyPattern.MatchString(key) {
			errs = append(errs, fmt.Errorf("enrichment key %q must match %s", key, keyPattern))
		}
		if e.Label == "" {
			errs = append(errs, fmt.Errorf("enrichment %q: label is required", key))
		}
		if !slices.Contains(enrichmentTypes, e.Type) {
			errs = append(errs, fmt.Errorf("enrichment %q: type %q must be one of %v", key, e.Type, enrichmentTypes))
		}
		if e.Type == "score" && (len(e.Range) != 2 || !(e.Range[0] < e.Range[1])) {
			errs = append(errs, fmt.Errorf("enrichment %q: score needs range = [low, high]", key))
		}
		if e.Priority != 0 && !e.Grid {
			errs = append(errs, fmt.Errorf("enrichment %q: priority needs grid", key))
		}
	}
	grids := 0
	for _, e := range m.Enrichments {
		if e.Grid {
			grids++
		}
	}
	if grids > 1 {
		errs = append(errs, errors.New("at most one enrichment can set grid"))
	}
	errs = append(errs, unique("format", m.Formats, matches(formatPattern))...)
	if slices.Contains(m.Formats, "json") {
		errs = append(errs, errors.New(`format "json" is the core's own CCv3 export`))
	}
	if m.EnrichMaxCards < 0 {
		errs = append(errs, errors.New("enrich_max_cards can't be negative"))
	}
	errs = append(errs, unique("input", m.Inputs, oneOf(importInputs))...)
	for _, key := range slices.Sorted(maps.Keys(m.Records)) {
		errs = append(errs, m.Records[key].validate(key)...)
	}
	if len(m.SourceKinds) > 0 && !m.PullOnly() {
		errs = append(errs, errors.New("source_kinds needs the pull capability and not push"))
	}
	errs = append(errs, unique("source kind", m.SourceKinds, matches(sourceKindPattern))...)
	errs = append(errs, unique("uses", m.Uses, oneOf(slices.Sorted(maps.Keys(Services))))...)
	for _, id := range slices.Sorted(maps.Keys(m.Settings.Actions)) {
		if !keyPattern.MatchString(id) {
			errs = append(errs, fmt.Errorf("settings action %q must match %s", id, keyPattern))
		}
		if strings.TrimSpace(m.Settings.Actions[id].Label) == "" {
			errs = append(errs, fmt.Errorf("settings action %q: label is required", id))
		}
	}
	return errors.Join(errs...)
}

// capabilityKeys are the manifest keys that belong to one capability. A key
// marked only is an error without its capability; a key marked required is
// an error when a plugin with the capability leaves it unset.
var capabilityKeys = []struct {
	capability, key string
	only, required  bool
	set             func(*Manifest) bool
}{
	{CapabilityTheme, "stylesheet", true, false, func(m *Manifest) bool { return m.Stylesheet != "" }},
	{CapabilityEnrich, "enrichments", false, true, func(m *Manifest) bool { return len(m.Enrichments) > 0 }},
	{CapabilityEnrich, "enrich_max_cards", true, false, func(m *Manifest) bool { return m.EnrichMaxCards != 0 }},
	{CapabilityCodec, "formats", true, true, func(m *Manifest) bool { return len(m.Formats) > 0 }},
	{CapabilityImport, "bulk", true, false, func(m *Manifest) bool { return m.Bulk }},
	{CapabilityImport, "inputs", true, false, func(m *Manifest) bool { return len(m.Inputs) > 0 }},
	{CapabilityRecords, "records", true, true, func(m *Manifest) bool { return len(m.Records) > 0 }},
	{CapabilitySettings, "settings.actions", true, false, func(m *Manifest) bool { return len(m.Settings.Actions) > 0 }},
}

func (m *Manifest) checkCapabilityKeys() []error {
	var errs []error
	for _, k := range capabilityKeys {
		has, set := m.Has(k.capability), k.set(m)
		switch {
		case k.only && set && !has:
			errs = append(errs, fmt.Errorf("%s needs the %s capability", k.key, k.capability))
		case k.required && has && !set:
			article := "a"
			if strings.ContainsAny(k.capability[:1], "aeiou") {
				article = "an"
			}
			errs = append(errs, fmt.Errorf("%s %s plugin must declare %s", article, k.capability, k.key))
		}
	}
	return errs
}

// unique reports each key that check rejects and each listed more than once.
// A nil check accepts any key.
func unique(what string, keys []string, check func(string) error) []error {
	var errs []error
	for i, k := range keys {
		if check != nil {
			if err := check(k); err != nil {
				errs = append(errs, fmt.Errorf("%s %q %w", what, k, err))
			}
		}
		if slices.Contains(keys[:i], k) {
			errs = append(errs, fmt.Errorf("%s %q listed twice", what, k))
		}
	}
	return errs
}

// matches is a check for unique that wants each key to match pattern.
func matches(pattern *regexp.Regexp) func(string) error {
	return func(k string) error {
		if !pattern.MatchString(k) {
			return fmt.Errorf("must match %s", pattern)
		}
		return nil
	}
}

// oneOf is a check for unique that wants each key to be one of allowed.
func oneOf(allowed []string) func(string) error {
	return func(k string) error {
		if !slices.Contains(allowed, k) {
			return fmt.Errorf("must be one of %v", allowed)
		}
		return nil
	}
}

// Settings is the manifest's [settings] table.
type Settings struct {
	// Actions are buttons on the instance form, by id; each runs settings.run.
	Actions map[string]SettingsAction `toml:"actions"`
}

// SettingsAction is one button on the instance form.
type SettingsAction struct {
	Label string `toml:"label"`
}

// CommandPath resolves the executable: relative paths containing / are
// inside the plugin folder, absolute paths are used as-is, and bare names
// are looked up on PATH.
func (m *Manifest) CommandPath() string {
	cmd := m.Command[0]
	if !filepath.IsAbs(cmd) && strings.Contains(cmd, "/") {
		return filepath.Join(m.Dir, cmd)
	}
	return cmd
}

// Has reports whether the plugin declares capability.
func (m *Manifest) Has(capability string) bool { return slices.Contains(m.Capabilities, capability) }

// AllowsManyInstances reports whether the owner may create more than one instance. Login plugins always may, so setup can replace a broken login.
func (m *Manifest) AllowsManyInstances() bool { return m.MultipleInstances || m.Has(CapabilityAuth) }

// IsTheme reports whether the plugin is a theme: a stylesheet and no process.
func (m *Manifest) IsTheme() bool { return m.Has(CapabilityTheme) }

// IsTarget reports whether the plugin syncs with an app: it pushes, pulls,
// or both.
func (m *Manifest) IsTarget() bool { return m.Has(CapabilityPush) || m.Has(CapabilityPull) }

// PullOnly reports whether the plugin pulls but never pushes, so its
// instances follow site sources instead of taking the library's copy.
func (m *Manifest) PullOnly() bool { return m.Has(CapabilityPull) && !m.Has(CapabilityPush) }

// StylesheetPath is the theme's CSS file.
func (m *Manifest) StylesheetPath() string { return filepath.Join(m.Dir, m.Stylesheet) }

func (m *Manifest) validateTheme() []error {
	var errs []error
	if len(m.Capabilities) > 1 {
		errs = append(errs, errors.New("theme can't be combined with other capabilities"))
	}
	if len(m.Command) > 0 {
		errs = append(errs, errors.New("a theme plugin has no command"))
	}
	for key, set := range map[string]bool{"database": m.Database, "instances": m.MultipleInstances, "secrets": len(m.Secrets) > 0,
		"config": len(m.Config) > 0, "enrichments": len(m.Enrichments) > 0} {
		if set {
			errs = append(errs, fmt.Errorf("a theme plugin has no %s", key))
		}
	}
	switch {
	case m.Stylesheet == "":
		errs = append(errs, errors.New("a theme plugin needs a stylesheet"))
	case !strings.HasSuffix(m.Stylesheet, ".css"):
		errs = append(errs, fmt.Errorf("stylesheet %q must end in .css", m.Stylesheet))
	case filepath.IsAbs(m.Stylesheet) || (m.Dir != "" && !inside(m.Dir, m.StylesheetPath())):
		errs = append(errs, fmt.Errorf("stylesheet %q is outside the plugin folder", m.Stylesheet))
	}
	return errs
}

// inside reports whether path is in dir.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}
