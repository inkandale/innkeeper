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
	build := exec.Command("go", "build", "-o", bin, "github.com/davidgibbons/innkeeper/internal/fakeplugin")
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
`, strings.Join(cmd, ", "), strings.Join(quoted, ", "))
	if slices.Contains(caps, "codec") {
		manifest += "formats = [\"fake\"]\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
