package protocol

import (
	"strings"
	"testing"
)

const itemsManifest = `
name = "forge"
version = "0.1.0"
protocol = 1
command = ["./forge"]
capabilities = ["action", "items"]

[items.skills]
label = "Skills"
import = ["file", "url"]

[items.skills.schema]
type = "object"
required = ["name"]
properties.name = {type = "string", title = "Name"}
properties.body = {type = "string", contentMediaType = "text/markdown"}
`

func TestManifestItems(t *testing.T) {
	m, err := LoadManifest(writeManifest(t, itemsManifest))
	if err != nil {
		t.Fatal(err)
	}
	skills := m.Items["skills"]
	if skills.Label != "Skills" || len(skills.Import) != 2 || skills.Schema["type"] != "object" {
		t.Fatalf("skills = %+v", skills)
	}
	tests := []struct{ name, replace, with, wantErr string }{
		{"capability without types", "[items.skills]\nlabel = \"Skills\"\nimport = [\"file\", \"url\"]\n\n[items.skills.schema]\ntype = \"object\"\nrequired = [\"name\"]\nproperties.name = {type = \"string\", title = \"Name\"}\nproperties.body = {type = \"string\", contentMediaType = \"text/markdown\"}\n", "", "must declare an item type"},
		{"types without capability", `"action", "items"]`, `"action"]`, "items needs the items capability"},
		{"bad type key", "[items.skills]", "[items.Skills]", `item type "Skills"`},
		{"no label", `label = "Skills"`, `label = ""`, "label is required"},
		{"schema not an object", `type = "object"`, `type = "string"`, "schema must be a JSON Schema with type object"},
		{"unknown import", `import = ["file", "url"]`, `import = ["file", "ftp"]`, `import "ftp"`},
		{"import twice", `import = ["file", "url"]`, `import = ["file", "file"]`, "listed twice"},
		{"unknown key", `label = "Skills"`, "label = \"Skills\"\nicon = \"x\"", "unknown keys: items.skills.icon"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.Replace(itemsManifest, tt.replace, tt.with, 1)
			if body == itemsManifest {
				t.Fatalf("replace target %q not found", tt.replace)
			}
			_, err := LoadManifest(writeManifest(t, body))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestItemSummaryValidate(t *testing.T) {
	if err := (ItemSummary{ID: "college", Name: "College"}).Validate(); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]ItemSummary{
		"no id":      {Name: "x"},
		"slash":      {ID: "a/b", Name: "x"},
		"long id":    {ID: strings.Repeat("a", 257), Name: "x"},
		"blank name": {ID: "a", Name: " "},
	} {
		if s.Validate() == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestSkillValidate(t *testing.T) {
	if err := (Skill{Name: "college-life"}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "College", "has space", strings.Repeat("a", 65)} {
		if (Skill{Name: name}).Validate() == nil {
			t.Errorf("%q: no error", name)
		}
	}
}
