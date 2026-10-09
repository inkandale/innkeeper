package contract

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/testutil"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func failed(results []Result) map[string]error {
	out := map[string]error{}
	for _, r := range results {
		if r.Err != nil {
			out[r.Name] = r.Err
		}
	}
	return out
}

// The check sends no config, so the fake's list methods return an error,
// which counts as an answer.
func TestWellBehavedPluginPasses(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePlugin(t), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 6 {
		t.Fatalf("ran %d checks, want 6", len(results))
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}
}

func TestMisbehavingPluginsFail(t *testing.T) {
	callTimeout = 300 * time.Millisecond
	t.Cleanup(func() { callTimeout = 5 * time.Second })
	tests := []struct{ mode, check, wantErr string }{
		{"wrong-caps", "initialize handshake matches manifest", "reported capabilities"},
		{"bad-errors", "unknown method returns -32601", "got success"},
		{"silent", "unknown method returns -32601", "no response within"},
		{"crash-unknown", "unknown method returns -32601", "plugin exited"},
		{"crash-unknown", "exits after shutdown", "already exited"},
		{"no-shutdown", "answers shutdown", "no response within"},
		{"bad-describe", "target.describe returns a valid description", "fields is empty"},
		{"bad-list", "a pull target answers target.list and target.list_lorebooks", "no remote_id"},
	}

	for _, tt := range tests {
		t.Run(tt.mode+"/"+tt.check, func(t *testing.T) {
			results, err := Check(context.Background(), testutil.FakePlugin(t, "-mode="+tt.mode), quiet)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := failed(results)[tt.check]
			if !ok {
				t.Fatalf("check %q passed, want failure; results: %v", tt.check, results)
			}
			if !strings.Contains(got.Error(), tt.wantErr) {
				t.Fatalf("check %q failed with %q, want it to mention %q", tt.check, got, tt.wantErr)
			}
		})
	}
}

func TestDescribePullNeedsCapability(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"push"}), quiet)
	if err != nil {
		t.Fatal(err)
	}
	err = failed(results)["target.describe returns a valid description"]
	if err == nil || !strings.Contains(err.Error(), "pull capability") {
		t.Fatalf("got %v, want a pull capability error", err)
	}
}

func TestCodecChecks(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"codec"}), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 6 {
		t.Fatalf("ran %d checks, want 6", len(results))
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}

	results, err = Check(context.Background(), testutil.FakePluginWith(t, []string{"codec"}, "-mode=bad-codec"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	f := failed(results)
	if len(f) != 2 || f[checkRoundTripName] == nil || f[checkRejectsName] == nil {
		t.Fatalf("failed = %v, want only the two codec checks", f)
	}

	tests := []struct{ mode, check, wantErr string }{
		{"codec-hard-link", checkRoundTripName, "hard links"},
		{"codec-no-avatar", checkRoundTripName, "can't embed"},
		{"codec-wrong-code", checkRejectsName, "reply -32602"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"codec"}, "-mode="+tt.mode), quiet)
			if err != nil {
				t.Fatal(err)
			}
			if got := failed(results)[tt.check]; got == nil || !strings.Contains(got.Error(), tt.wantErr) {
				t.Fatalf("check %q: got %v, want %q", tt.check, got, tt.wantErr)
			}
		})
	}
}

func TestToolChecks(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"mcp"}), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err, ok := failed(results)["mcp.tools returns valid tools"]; ok {
		t.Fatalf("good tools failed: %v", err)
	}
	results, err = Check(context.Background(), testutil.FakePluginWith(t, []string{"mcp"}, "-mode=bad-tools"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed(results)["mcp.tools returns valid tools"]; err == nil || !strings.Contains(err.Error(), "lowercase letters") {
		t.Fatalf("bad tools: %v", err)
	}
}

func TestSetupCheck(t *testing.T) {
	const name = "auth.setup returns a form or -32601"
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"auth"}), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}
	if !slices.ContainsFunc(results, func(r Result) bool { return r.Name == name }) {
		t.Fatalf("no %q check in %v", name, results)
	}

	results, err = Check(context.Background(), testutil.FakePluginWith(t, []string{"auth"}, "-mode=bad-setup"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed(results)[name]; err == nil || !strings.Contains(err.Error(), "not a JSON object") {
		t.Fatalf("bad setup form: got %v", err)
	}
}

func TestCatalogChecks(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"catalog"}), quiet)
	if err != nil {
		t.Fatal(err)
	}
	// initialize, unknown method, six catalog checks, and two for shutdown.
	if len(results) != 10 {
		t.Fatalf("ran %d checks, want 10: %v", len(results), results)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}

	tests := []struct{ mode, check, wantErr string }{
		{"bad-catalog", catalogDescribeName, "kinds is empty"},
		{"catalog-repeat", catalogPagesName, "came back on a later page"},
		{"catalog-lax", catalogFiltersName, "got success"},
		{"catalog-no-notfound", catalogNotFoundName, "expected error -32004, got -32000"},
		{"catalog-wrong-hash", catalogGetName, "hash with the cardhash package"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"catalog"}, "-mode="+tt.mode), quiet)
			if err != nil {
				t.Fatal(err)
			}
			if got := failed(results)[tt.check]; got == nil || !strings.Contains(got.Error(), tt.wantErr) {
				t.Fatalf("check %q: got %v, want %q", tt.check, got, tt.wantErr)
			}
		})
	}
}

// Without a config, a catalog may refuse what needs its source. With one,
// it must search.
func TestSourcelessCatalog(t *testing.T) {
	dir := testutil.FakePluginWith(t, []string{"catalog"}, "-mode=catalog-sourceless")
	results, err := Check(context.Background(), dir, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed without a config: %v", f)
	}

	Config = map[string]any{"source": "testdata"}
	t.Cleanup(func() { Config = nil })
	results, err = Check(context.Background(), dir, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if got := failed(results)[catalogPagesName]; got == nil || !strings.Contains(got.Error(), "config.source is not set") {
		t.Fatalf("with a config: got %v, want the search error", got)
	}
}
