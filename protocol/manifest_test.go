package protocol

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const validManifest = `
name = "lumiverse"
version = "0.1.0"
protocol = 1
command = ["./lumiverse-plugin", "--quiet"]
capabilities = ["push", "pull", "search"]
secrets = ["api_key"]

[config]
type = "object"

[enrichments]
summary = { type = "text", label = "AI summary" }
quality = { type = "score", range = [1, 5], label = "Quality" }
`

func TestLoadManifest(t *testing.T) {
	dir := writeManifest(t, validManifest)
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "lumiverse" || len(m.Capabilities) != 3 || m.Secrets[0] != "api_key" {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if got := m.Enrichments["quality"].Range; len(got) != 2 || got[1] != 5 {
		t.Fatalf("quality range = %v, want [1 5]", got)
	}
	if got, want := m.CommandPath(), filepath.Join(m.Dir, "lumiverse-plugin"); got != want {
		t.Fatalf("CommandPath = %q, want %q", got, want)
	}
}

func TestCommandPath(t *testing.T) {
	tests := []struct{ cmd, want string }{
		{"python3", "python3"},
		{"./run", "/plugins/x/run"},
		{"bin/run", "/plugins/x/bin/run"},
		{"/usr/bin/node", "/usr/bin/node"},
	}
	for _, tt := range tests {
		m := &Manifest{Command: []string{tt.cmd}, Dir: "/plugins/x"}
		if got := m.CommandPath(); got != tt.want {
			t.Errorf("CommandPath(%q) = %q, want %q", tt.cmd, got, tt.want)
		}
	}
}

func TestMissingManifest(t *testing.T) {
	if _, err := LoadManifest(t.TempDir()); err == nil || !strings.Contains(err.Error(), "read manifest") {
		t.Fatalf("got %v, want read manifest error", err)
	}
}

func TestManifestErrors(t *testing.T) {
	tests := []struct {
		name, replace, with, wantErr string
	}{
		{"bad name", `name = "lumiverse"`, `name = "Lumi Verse"`, "name"},
		{"wrong protocol", `protocol = 1`, `protocol = 2`, "protocol 2 is not supported"},
		{"unknown capability", `"search"]`, `"teleport"]`, `unknown capability "teleport"`},
		{"duplicate capability", `"search"]`, `"push"]`, "listed twice"},
		{"score without range", `range = [1, 5], `, ``, "score needs range"},
		{"bad enrichment type", `type = "text"`, `type = "essay"`, `type "essay"`},
		{"unknown key", `secrets = ["api_key"]`, `secret = ["api_key"]`, "unknown keys: secret"},
		{"trailing hyphen", `name = "lumiverse"`, `name = "lumiverse-"`, "name"},
		{"name too long", `name = "lumiverse"`, `name = "` + strings.Repeat("a", 42) + `"`, "at most 41"},
		{"missing version", `version = "0.1.0"`, ``, "version is required"},
		{"empty command", `command = ["./lumiverse-plugin", "--quiet"]`, `command = []`, "command is required"},
		{"command escapes folder", `"./lumiverse-plugin"`, `"./../../evil"`, "outside the plugin folder"},
		{"empty secret", `secrets = ["api_key"]`, `secrets = [""]`, `secret name ""`},
		{"duplicate secret", `secrets = ["api_key"]`, `secrets = ["api_key", "api_key"]`, "listed twice"},
		{"bad enrichment key", `summary = {`, `"bad key!" = {`, `enrichment key "bad key!"`},
		{"enrichment without label", `, label = "AI summary"`, ``, "label is required"},
		{"nan range", `range = [1, 5]`, `range = [nan, nan]`, "score needs range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.Replace(validManifest, tt.replace, tt.with, 1)
			if body == validManifest {
				t.Fatalf("replace target %q not found", tt.replace)
			}
			_, err := LoadManifest(writeManifest(t, body))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestFormats(t *testing.T) {
	manifest := func(caps []string, formats ...string) *Manifest {
		return &Manifest{Name: "c", Version: "1", Protocol: Version, Command: []string{"c"}, Capabilities: caps, Formats: formats}
	}
	if err := manifest([]string{"codec"}, "png", "json-v2").Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]*Manifest{
		"codec without formats": manifest([]string{"codec"}),
		"formats without codec": manifest([]string{"push"}, "png"),
		"uppercase":             manifest([]string{"codec"}, "PNG"),
		"json is the core's":    manifest([]string{"codec"}, "json"),
		"listed twice":          manifest([]string{"codec"}, "png", "png"),
	}
	for name, m := range bad {
		if m.Validate() == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestLoadManifestNestedConfig(t *testing.T) {
	dir := t.TempDir()
	toml := `name = "c"
version = "1"
protocol = 1
command = ["c"]
capabilities = ["import"]

[config]
type = "object"

[config.properties.folder]
type = "string"
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	props, _ := m.Config["properties"].(map[string]any)
	if _, ok := props["folder"]; !ok {
		t.Fatalf("config = %v; want properties.folder", m.Config)
	}

	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(toml+"\n[extra]\nx = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("an unknown top-level table loaded")
	}
}

const themeManifest = `
name = "dusk"
version = "1.0.0"
protocol = 1
capabilities = ["theme"]
stylesheet = "theme.css"
`

func TestThemeManifest(t *testing.T) {
	dir := writeManifest(t, themeManifest)
	if err := os.WriteFile(filepath.Join(dir, "theme.css"), []byte(":root{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsTheme() || m.StylesheetPath() != filepath.Join(m.Dir, "theme.css") {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestThemeManifestErrors(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"missing file", themeManifest, "stylesheet"},
		{"with command", themeManifest + `command = ["./x"]` + "\n", "a theme plugin has no command"},
		{"no stylesheet", strings.Replace(themeManifest, `stylesheet = "theme.css"`, "", 1), "a theme plugin needs a stylesheet"},
		{"outside folder", strings.Replace(themeManifest, `"theme.css"`, `"../theme.css"`, 1), "outside the plugin folder"},
		{"not css", strings.Replace(themeManifest, `"theme.css"`, `"theme.js"`, 1), "must end in .css"},
		{"mixed", strings.Replace(themeManifest, `["theme"]`, `["theme", "push"]`, 1), "theme can't be combined"},
		{"stylesheet without theme", validManifest[:strings.Index(validManifest, "[config]")] + `stylesheet = "a.css"` + "\n", "stylesheet needs the theme capability"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadManifest(writeManifest(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}
