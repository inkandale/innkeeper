// Package testutil holds helpers shared by core tests.
package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// FakeEnrichments are the keys the fake plugin's enrich.run writes.
const FakeEnrichments = `[enrichments]
quality = { type = "score", range = [0, 100], label = "Quality", grid = true }
voice = { type = "score", range = [0, 100], label = "Voice" }
summary = { type = "text", label = "Summary" }
`

// FakePlugin builds internal/fakeplugin into a temp plugin folder and writes
// a manifest that runs it with args. It returns the folder.
func FakePlugin(t *testing.T, args ...string) string {
	t.Helper()
	return FakePluginWith(t, []string{"push", "pull"}, args...)
}

// FakePluginWith is FakePlugin with the given capabilities.
func FakePluginWith(t *testing.T, caps []string, args ...string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakeplugin")
	build := exec.Command("go", "build", "-o", bin, "github.com/inkandale/innkeeper/internal/fakeplugin")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fakeplugin: %v\n%s", err, out)
	}
	cmd := append([]string{"./fakeplugin", "-caps=" + strings.Join(caps, ",")}, args...)
	for i, c := range cmd {
		cmd[i] = strconv.Quote(c)
	}
	quoted := make([]string, len(caps))
	for i, c := range caps {
		quoted[i] = strconv.Quote(c)
	}
	manifest := fmt.Sprintf(`name = "fake"
version = "0.0.1"
protocol = 1
command = [%s]
capabilities = [%s]
multiple_instances = true
`, strings.Join(cmd, ", "), strings.Join(quoted, ", "))
	if slices.Contains(caps, "codec") {
		manifest += "formats = [\"fake\"]\n"
	}
	if slices.Contains(args, "-mode=database") {
		manifest += "database = true\n"
	}
	if slices.Contains(args, "-mode=bulk") {
		manifest += "bulk = true\n"
	}
	// Tables go last; keys after one would belong to it. An importer declares
	// enrichments too, for emit.card's values.
	if slices.Contains(caps, "enrich") || slices.Contains(caps, "import") {
		manifest += FakeEnrichments
	}
	if slices.Contains(caps, "records") {
		manifest += `[records.notes]
label = "Notes"
import = ["file", "url"]
schema = {type = "object", required = ["title"], properties = {title = {type = "string"}, body = {type = "string", contentMediaType = "text/markdown"}}}
`
	}
	if slices.Contains(caps, "settings") {
		manifest += `[settings.actions]
test = { label = "Test connection" }
`
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// FakeConsumer is a fake with caps that declares uses = ["ai.chat"]. Its
// actions, tools, imports, record lists, and settings actions call ai.chat on
// the instance its connection config names.
func FakeConsumer(t *testing.T, caps []string, args ...string) string {
	t.Helper()
	dir := FakePluginWith(t, caps, args...)
	path := filepath.Join(dir, "plugin.toml")
	manifest, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// uses goes first, since a key after a table belongs to it.
	manifest = append([]byte(`uses = ["ai.chat"]
`), manifest...)
	manifest = append(manifest, `
[config]
type = "object"

[config.properties.connection]
type = "integer"
format = "instance"
x-offers = "ai.chat"
`...)
	if err := os.WriteFile(path, manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
