package protocol

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Capabilities lists every capability a plugin may declare.
var Capabilities = []string{"import", "search", "push", "pull", "action", "enrich", "codec", "auth"}

var enrichmentTypes = []string{"text", "score", "list"}

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

	// Dir is the absolute path of the plugin's folder.
	Dir string `toml:"-"`
}

// Enrichment declares one enrichment key a plugin writes and how the UI
// shows it.
type Enrichment struct {
	Type  string    `toml:"type"`
	Label string    `toml:"label"`
	Range []float64 `toml:"range"`
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
		// The decoder lists nested config tables as undecoded though they are in the map.
		if k[0] != "config" {
			unknown = append(unknown, k.String())
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("manifest %s: unknown keys: %s", abs, strings.Join(unknown, ", "))
	}
	m.Dir = abs
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("manifest %s: %w", abs, err)
	}
	return &m, nil
}

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
	if len(m.Command) == 0 || m.Command[0] == "" {
		errs = append(errs, errors.New("command is required"))
	} else if m.Dir != "" && !filepath.IsAbs(m.Command[0]) && strings.Contains(m.Command[0], "/") {
		if rel, err := filepath.Rel(m.Dir, m.CommandPath()); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			errs = append(errs, fmt.Errorf("command %q is outside the plugin folder", m.Command[0]))
		}
	}
	if len(m.Capabilities) == 0 {
		errs = append(errs, errors.New("at least one capability is required"))
	}
	seen := map[string]bool{}
	for _, c := range m.Capabilities {
		if !slices.Contains(Capabilities, c) {
			errs = append(errs, fmt.Errorf("unknown capability %q", c))
		}
		if seen[c] {
			errs = append(errs, fmt.Errorf("capability %q listed twice", c))
		}
		seen[c] = true
	}
	seenSecrets := map[string]bool{}
	for _, s := range m.Secrets {
		if !keyPattern.MatchString(s) {
			errs = append(errs, fmt.Errorf("secret name %q must match %s", s, keyPattern))
		}
		if seenSecrets[s] {
			errs = append(errs, fmt.Errorf("secret %q listed twice", s))
		}
		seenSecrets[s] = true
	}
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
	}
	hasCodec := slices.Contains(m.Capabilities, "codec")
	if hasCodec && len(m.Formats) == 0 {
		errs = append(errs, errors.New("a codec plugin must list its formats"))
	}
	if !hasCodec && len(m.Formats) > 0 {
		errs = append(errs, errors.New("formats needs the codec capability"))
	}
	seenFormats := map[string]bool{}
	for _, f := range m.Formats {
		if !formatPattern.MatchString(f) {
			errs = append(errs, fmt.Errorf("format %q must match %s", f, formatPattern))
		}
		if f == "json" {
			errs = append(errs, errors.New(`format "json" is the core's own CCv3 export`))
		}
		if seenFormats[f] {
			errs = append(errs, fmt.Errorf("format %q listed twice", f))
		}
		seenFormats[f] = true
	}
	return errors.Join(errs...)
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
