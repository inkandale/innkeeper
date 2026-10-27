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
title = "Lumiverse"
version = "0.1.0"
protocol = 1
command = ["./lumiverse-plugin", "--quiet"]
capabilities = ["push", "pull"]
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
	if m.Name != "lumiverse" || m.DisplayName() != "Lumiverse" || len(m.Capabilities) != 2 || m.Secrets[0] != "api_key" {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if got := m.Enrichments["quality"].Range; len(got) != 2 || got[1] != 5 {
		t.Fatalf("quality range = %v, want [1 5]", got)
	}
	if got, want := m.CommandPath(), filepath.Join(m.Dir, "lumiverse-plugin"); got != want {
		t.Fatalf("CommandPath = %q, want %q", got, want)
	}
}

func TestDisplayNameFallsBackToName(t *testing.T) {
	if got := (&Manifest{Name: "card-import"}).DisplayName(); got != "card-import" {
		t.Fatalf("DisplayName = %q, want card-import", got)
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
		{"unknown capability", `"pull"]`, `"pull", "teleport"]`, `unknown capability "teleport"`},
		{"duplicate capability", `"pull"]`, `"pull", "push"]`, "listed twice"},
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
		{"two grid keys", `summary = { type = "text", label = "AI summary" }`,
			`summary = { type = "text", label = "AI summary", grid = true }
extra = { type = "text", label = "Extra", grid = true }`, "at most one enrichment can set grid"},
		{"priority off the grid", `label = "AI summary" }`, `label = "AI summary", priority = 100 }`, "priority needs grid"},
		{"unknown use", `secrets = ["api_key"]`, `secrets = ["api_key"]
uses = ["ai.draw"]`, `uses "ai.draw"`},
		{"config not an object schema", "[config]\ntype = \"object\"", "[config]\ntype = \"string\"", "config must be a JSON Schema with type object"},
		{"config without a type", "[config]\ntype = \"object\"", "[config]\ntitle = \"Settings\"", "config must be a JSON Schema with type object"},
		{"duplicate use", `secrets = ["api_key"]`, `secrets = ["api_key"]
uses = ["ai.chat", "ai.chat"]`, `uses "ai.chat" listed twice`},
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

func TestInputs(t *testing.T) {
	manifest := func(caps []string, inputs ...string) *Manifest {
		return &Manifest{Name: "i", Version: "1", Protocol: Version, Command: []string{"i"}, Capabilities: caps, Inputs: inputs}
	}
	if err := manifest([]string{"import"}, "file", "url", "folder").Validate(); err != nil {
		t.Fatal(err)
	}
	if err := manifest([]string{"import"}).Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]*Manifest{
		"inputs without import": manifest([]string{"push"}, "url"),
		"unknown input":         manifest([]string{"import"}, "dir"),
		"listed twice":          manifest([]string{"import"}, "url", "url"),
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
		{"database", themeManifest + "database = true\n", "a theme plugin has no database"},
		{"multiple instances", themeManifest + "multiple_instances = true\n", "a theme plugin has no instances"},
		{"secrets", themeManifest + `secrets = ["k"]` + "\n", "a theme plugin has no secrets"},
		{"config", themeManifest + "[config]\nk = \"v\"\n", "a theme plugin has no config"},
		{"enrichments", themeManifest + "[enrichments.x]\n", "a theme plugin has no enrichments"},
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

func TestThemeStylesheetConfined(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.css")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, setup := range map[string]func(css string) error{
		"symlink":   func(css string) error { return os.Symlink(outside, css) },
		"directory": func(css string) error { return os.Mkdir(css, 0o755) },
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeManifest(t, themeManifest)
			if err := setup(filepath.Join(dir, "theme.css")); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadManifest(dir); err == nil || !strings.Contains(err.Error(), "stylesheet") {
				t.Fatalf("err = %v, want a stylesheet error", err)
			}
		})
	}
}

func TestLoadManifestIgnoresRetiredSearch(t *testing.T) {
	dir := writeManifest(t, strings.Replace(validManifest, `["push", "pull"]`, `["push", "search", "pull"]`, 1))
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.Capabilities, ",") != "push,pull" {
		t.Fatalf("capabilities = %v, want [push pull]", m.Capabilities)
	}
}

func TestManifestHasOnlyDeclaredCapabilities(t *testing.T) {
	m := &Manifest{Capabilities: []string{"push", "pull"}}
	if !m.Has("pull") || m.Has("import") {
		t.Fatalf("Has(pull) = %v, Has(import) = %v; want true, false", m.Has("pull"), m.Has("import"))
	}
}

func TestManifestIcon(t *testing.T) {
	dir := writeManifest(t, `icon = "icon.svg"`+"\n"+validManifest)
	if err := os.WriteFile(filepath.Join(dir, "icon.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Icon != "icon.svg" || m.IconType() != "image/svg+xml" {
		t.Fatalf("icon = %q, type %q", m.Icon, m.IconType())
	}
}

func TestManifestIconErrors(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, icon, want string
		setup            func(dir string) error
	}{
		{"missing file", "icon.png", "icon", nil},
		{"not an image", "icon.js", "icon \"icon.js\" must end in", nil},
		{"outside folder", "../icon.png", "outside the plugin folder", nil},
		{"symlink out", "icon.png", "icon", func(dir string) error { return os.Symlink(outside, filepath.Join(dir, "icon.png")) }},
		{"directory", "icon.png", "icon", func(dir string) error { return os.Mkdir(filepath.Join(dir, "icon.png"), 0o755) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeManifest(t, `icon = "`+tt.icon+`"`+"\n"+validManifest)
			if tt.setup != nil {
				if err := tt.setup(dir); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := LoadManifest(dir); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestManifestSourceKinds(t *testing.T) {
	base := Manifest{Name: "url-import", Version: "0.3.0", Protocol: Version, Command: []string{"./url-import"}}
	tests := []struct {
		name    string
		caps    []string
		kinds   []string
		wantErr string
	}{
		{"pull-only follower", []string{"import", "sites", "pull"}, []string{"chub", "character-tavern"}, ""},
		{"no pull", []string{"import"}, []string{"chub"}, "source_kinds needs the pull capability and not push"},
		{"an app", []string{"push", "pull"}, []string{"chub"}, "source_kinds needs the pull capability and not push"},
		{"bad kind", []string{"pull"}, []string{"Chub"}, `source kind "Chub"`},
		{"twice", []string{"pull"}, []string{"chub", "chub"}, `source kind "chub" listed twice`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base
			m.Capabilities, m.SourceKinds = tt.caps, tt.kinds
			err := m.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Validate = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Validate = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestEnrichNeedsEnrichments(t *testing.T) {
	m := &Manifest{Name: "judge", Version: "1", Protocol: Version, Command: []string{"judge"}, Capabilities: []string{"enrich"}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "an enrich plugin must declare enrichments") {
		t.Fatalf("got %v", err)
	}
	m.Enrichments = map[string]Enrichment{"quality": {Type: "score", Label: "Quality", Range: []float64{0, 100}, Grid: true}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestManifestUses(t *testing.T) {
	body := strings.Replace(validManifest, `secrets = ["api_key"]`, `secrets = ["api_key"]
uses = ["ai.chat", "ai.embed"]`, 1)
	m, err := LoadManifest(writeManifest(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.Uses, ",") != "ai.chat,ai.embed" {
		t.Fatalf("Uses = %v", m.Uses)
	}
	ai := &Manifest{Name: "a", Version: "1", Protocol: Version, Command: []string{"a"}, Capabilities: []string{"ai"}}
	if err := ai.Validate(); err != nil {
		t.Fatalf("ai capability: %v", err)
	}
}

func TestManifestSettingsActions(t *testing.T) {
	base := `
name = "ai"
version = "0.1.0"
protocol = 1
command = ["./ai"]
`
	m, err := LoadManifest(writeManifest(t, base+`capabilities = ["settings"]

[settings.actions]
test = { label = "Test connection" }
`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Settings.Actions["test"].Label != "Test connection" {
		t.Fatalf("actions = %+v", m.Settings.Actions)
	}
	for name, body := range map[string]string{
		"no capability": `capabilities = ["action"]
[settings.actions]
test = { label = "Test" }
`,
		"no label": `capabilities = ["settings"]
[settings.actions]
test = {}
`,
		"bad key": `capabilities = ["settings"]
[settings.actions]
Test-It = { label = "Test" }
`,
	} {
		if _, err := LoadManifest(writeManifest(t, base+body)); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

func TestManifestListing(t *testing.T) {
	body := validManifest[:strings.Index(validManifest, "[config]")] +
		"multiple_instances = true\ndescription = \"Pushes cards.\"\ncategory = \"Apps\"\n"
	m, err := LoadManifest(writeManifest(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if !m.MultipleInstances || m.Description != "Pushes cards." || m.Category != "Apps" {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestManifestDescriptionIsOneLine(t *testing.T) {
	body := validManifest[:strings.Index(validManifest, "[config]")] + "description = \"one\\ntwo\"\n"
	if _, err := LoadManifest(writeManifest(t, body)); err == nil || !strings.Contains(err.Error(), "description must be one line") {
		t.Fatalf("err = %v, want description must be one line", err)
	}
}

func TestAllowsManyInstances(t *testing.T) {
	for _, c := range []struct {
		name string
		m    Manifest
		want bool
	}{
		{"plain", Manifest{Capabilities: []string{"push"}}, false},
		{"flagged", Manifest{Capabilities: []string{"push"}, MultipleInstances: true}, true},
		{"login without the flag", Manifest{Capabilities: []string{"auth"}}, true},
	} {
		if got := c.m.AllowsManyInstances(); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTargetAndPullOnly(t *testing.T) {
	for _, c := range []struct {
		caps             []string
		target, pullOnly bool
	}{
		{[]string{"push"}, true, false},
		{[]string{"push", "pull"}, true, false},
		{[]string{"pull"}, true, true},
		{[]string{"import"}, false, false},
	} {
		m := Manifest{Capabilities: c.caps}
		if m.IsTarget() != c.target || m.PullOnly() != c.pullOnly {
			t.Errorf("%v: IsTarget %v, PullOnly %v; want %v, %v", c.caps, m.IsTarget(), m.PullOnly(), c.target, c.pullOnly)
		}
	}
}

func TestBulkNeedsImport(t *testing.T) {
	m, err := LoadManifest(writeManifest(t, `name = "archive"
version = "1"
protocol = 1
command = ["./archive"]
capabilities = ["import"]
bulk = true
`))
	if err != nil || !m.Bulk {
		t.Fatalf("bulk import manifest = %+v, %v", m, err)
	}
	push := &Manifest{Name: "p", Version: "1", Protocol: Version, Command: []string{"p"}, Capabilities: []string{"push"}, Bulk: true}
	if push.Validate() == nil {
		t.Fatal("bulk without import validated")
	}
}

func TestEnrichMaxCardsTakesOnlyAnEnrichersNonNegativeCap(t *testing.T) {
	base := func(caps string, max int) *Manifest {
		m := &Manifest{Name: "p", Version: "1", Protocol: Version, Command: []string{"p"}, Capabilities: []string{caps}, EnrichMaxCards: max}
		if caps == "enrich" {
			m.Enrichments = map[string]Enrichment{"lint": {Type: "score", Label: "Lint", Range: []float64{0, 100}}}
		}
		return m
	}
	if err := base("enrich", 200_000).Validate(); err != nil {
		t.Errorf("enrich plugin raising its cap: %v", err)
	}
	if base("enrich", -1).Validate() == nil {
		t.Error("negative enrich_max_cards validated")
	}
	if base("push", 5).Validate() == nil {
		t.Error("enrich_max_cards without enrich validated")
	}
}

func TestManifestCapabilityKeys(t *testing.T) {
	record := map[string]RecordType{"skills": {Label: "Skills", Schema: map[string]any{"type": "object"}}}
	tests := []struct {
		name    string
		caps    []string
		set     func(*Manifest)
		wantErr string
	}{
		{"stylesheet without theme", []string{"push"}, func(m *Manifest) { m.Stylesheet = "a.css" }, "stylesheet needs the theme capability"},
		{"enrich without enrichments", []string{"enrich"}, func(*Manifest) {}, "an enrich plugin must declare enrichments"},
		{"enrich_max_cards without enrich", []string{"push"}, func(m *Manifest) { m.EnrichMaxCards = 5 }, "enrich_max_cards needs the enrich capability"},
		{"codec without formats", []string{"codec"}, func(*Manifest) {}, "a codec plugin must declare formats"},
		{"formats without codec", []string{"push"}, func(m *Manifest) { m.Formats = []string{"png"} }, "formats needs the codec capability"},
		{"bulk without import", []string{"push"}, func(m *Manifest) { m.Bulk = true }, "bulk needs the import capability"},
		{"inputs without import", []string{"push"}, func(m *Manifest) { m.Inputs = []string{"url"} }, "inputs needs the import capability"},
		{"records without types", []string{"records"}, func(*Manifest) {}, "a records plugin must declare records"},
		{"record types without records", []string{"push"}, func(m *Manifest) { m.Records = record }, "records needs the records capability"},
		{"settings actions without settings", []string{"push"}, func(m *Manifest) {
			m.Settings.Actions = map[string]SettingsAction{"test": {Label: "Test"}}
		}, "settings.actions needs the settings capability"},
		{"enrichments on an importer", []string{"import"}, func(m *Manifest) {
			m.Enrichments = map[string]Enrichment{"rating": {Type: "text", Label: "Rating"}}
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manifest{Name: "p", Version: "1", Protocol: Version, Command: []string{"p"}, Capabilities: tt.caps}
			tt.set(m)
			err := m.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Validate = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Validate = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
