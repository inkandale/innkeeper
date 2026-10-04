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

// Names become PostgreSQL schema names (plugin_<name>, with - as _), so keep
// them short and lowercase.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)

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
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("manifest %s: unknown keys: %s", abs, strings.Join(keys, ", "))
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
	if !namePattern.MatchString(m.Name) {
		errs = append(errs, fmt.Errorf("name %q must match %s", m.Name, namePattern))
	}
	if m.Version == "" {
		errs = append(errs, errors.New("version is required"))
	}
	if m.Protocol != Version {
		errs = append(errs, fmt.Errorf("protocol %d is not supported; this core speaks %d", m.Protocol, Version))
	}
	if len(m.Command) == 0 || m.Command[0] == "" {
		errs = append(errs, errors.New("command is required"))
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
	for key, e := range m.Enrichments {
		if !slices.Contains(enrichmentTypes, e.Type) {
			errs = append(errs, fmt.Errorf("enrichment %q: type %q must be one of %v", key, e.Type, enrichmentTypes))
		}
		if e.Type == "score" && (len(e.Range) != 2 || e.Range[0] >= e.Range[1]) {
			errs = append(errs, fmt.Errorf("enrichment %q: score needs range = [low, high]", key))
		}
	}
	return errors.Join(errs...)
}

// CommandPath resolves the executable: paths starting with ./ or ../ are
// relative to the plugin folder; bare names are looked up on PATH.
func (m *Manifest) CommandPath() string {
	cmd := m.Command[0]
	if strings.HasPrefix(cmd, "./") || strings.HasPrefix(cmd, "../") {
		return filepath.Join(m.Dir, cmd)
	}
	return cmd
}
