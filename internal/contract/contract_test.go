package contract

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/testutil"
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

// The check sends no config, so the fake's list and delete methods return an error,
// which counts as an answer.
func TestWellBehavedPluginPasses(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePlugin(t), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 8 {
		t.Fatalf("ran %d checks, want 8", len(results))
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
		{"bad-list", checkListsName, "no remote_id"},
	}

	for _, tt := range tests {
		t.Run(tt.mode+"/"+tt.check, func(t *testing.T) {
			results, err := Check(context.Background(), testutil.FakePlugin(t, "-mode="+tt.mode), Options{}, quiet)
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
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"push"}), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	err = failed(results)["target.describe returns a valid description"]
	if err == nil || !strings.Contains(err.Error(), "pull capability") {
		t.Fatalf("got %v, want a pull capability error", err)
	}
}

func TestCodecChecks(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"codec"}), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 6 {
		t.Fatalf("ran %d checks, want 6", len(results))
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}

	results, err = Check(context.Background(), testutil.FakePluginWith(t, []string{"codec"}, "-mode=bad-codec"), Options{}, quiet)
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
			results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"codec"}, "-mode="+tt.mode), Options{}, quiet)
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
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"mcp"}), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("good tools failed: %v", f)
	}
	if !slices.ContainsFunc(results, func(r Result) bool { return r.Name == checkMCPCallsName }) {
		t.Fatalf("no mcp.call check in %v", results)
	}
	tests := []struct{ mode, check, wantErr string }{
		{"bad-tools", checkMCPToolsName, "lowercase letters"},
		{"no-tool-call", checkMCPCallsName, "-32601"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"mcp"}, "-mode="+tt.mode), Options{}, quiet)
			if err != nil {
				t.Fatal(err)
			}
			if got := failed(results)[tt.check]; got == nil || !strings.Contains(got.Error(), tt.wantErr) {
				t.Fatalf("check %q: got %v, want %q", tt.check, got, tt.wantErr)
			}
		})
	}
}

func TestPushTargetMustNameWhatItPut(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"push", "pull"}, "-mode=bad-put"), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if got := failed(results)[checkPutName]; got == nil || !strings.Contains(got.Error(), "remote_id is empty") {
		t.Fatalf("got %v, want an empty remote_id failure", got)
	}
}

// With a store, the put check puts the card and its standalone lorebook,
// and deletes both.
func TestPutCheckCleansUpWhatItPut(t *testing.T) {
	dir := t.TempDir()
	opts := Options{Config: map[string]any{"dir": dir, "lorebooks": "standalone"}}
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"push", "pull"}), opts, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(left) > 0 {
		t.Fatalf("the check left %v", left)
	}
}

func TestImportChecks(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"import"}), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}
	if !slices.ContainsFunc(results, func(r Result) bool { return r.Name == checkImportName }) {
		t.Fatalf("no import check in %v", results)
	}
	results, err = Check(context.Background(), testutil.FakePluginWith(t, []string{"import"}, "-mode=no-import"), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if got := failed(results)[checkImportName]; got == nil || !strings.Contains(got.Error(), "-32601") {
		t.Fatalf("an importer without import.run: got %v, want a -32601 failure", got)
	}
}

func TestSettingsChecks(t *testing.T) {
	for _, mode := range []string{"", "-mode=settings-no-defaults"} {
		results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"settings"}, mode), Options{}, quiet)
		if err != nil {
			t.Fatal(err)
		}
		if f := failed(results); len(f) > 0 {
			t.Fatalf("%q: checks failed: %v", mode, f)
		}
		for _, name := range []string{checkSettingsDefaultsName, checkSettingsRunName} {
			if !slices.ContainsFunc(results, func(r Result) bool { return r.Name == name }) {
				t.Errorf("%q: no %q check in %v", mode, name, results)
			}
		}
	}
	tests := []struct{ mode, check, wantErr string }{
		{"settings-defaults-error", checkSettingsDefaultsName, "no defaults today"},
		{"no-settings-run", checkSettingsRunName, "test: "},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"settings"}, "-mode="+tt.mode), Options{}, quiet)
			if err != nil {
				t.Fatal(err)
			}
			if got := failed(results)[tt.check]; got == nil || !strings.Contains(got.Error(), tt.wantErr) {
				t.Fatalf("check %q: got %v, want %q", tt.check, got, tt.wantErr)
			}
		})
	}
}

func TestSetupCheck(t *testing.T) {
	const name = "auth.setup returns a form or -32601"
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"auth"}), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}
	if !slices.ContainsFunc(results, func(r Result) bool { return r.Name == name }) {
		t.Fatalf("no %q check in %v", name, results)
	}

	results, err = Check(context.Background(), testutil.FakePluginWith(t, []string{"auth"}, "-mode=bad-setup"), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed(results)[name]; err == nil || !strings.Contains(err.Error(), "type object") {
		t.Fatalf("bad setup form: got %v", err)
	}
	if err := failed(results)["auth.begin returns a form or a redirect"]; err == nil || !strings.Contains(err.Error(), "type object") {
		t.Fatalf("bad login form: got %v", err)
	}
}

func TestCatalogChecks(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"catalog"}), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	// initialize, unknown method, five catalog checks, and two for shutdown.
	if len(results) != 9 {
		t.Fatalf("ran %d checks, want 9: %v", len(results), results)
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
			results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"catalog"}, "-mode="+tt.mode), Options{}, quiet)
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
	results, err := Check(context.Background(), dir, Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed without a config: %v", f)
	}

	results, err = Check(context.Background(), dir, Options{Config: map[string]any{"source": "testdata"}}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if got := failed(results)[catalogPagesName]; got == nil || !strings.Contains(got.Error(), "config.source is not set") {
		t.Fatalf("with a config: got %v, want the search error", got)
	}
}

func TestSitesChecks(t *testing.T) {
	callTimeout = 300 * time.Millisecond
	t.Cleanup(func() { callTimeout = 5 * time.Second })
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"sites"}), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 10 {
		t.Fatalf("ran %d checks, want 10", len(results))
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}

	tests := []struct{ mode, check, wantErr string }{
		{"sites-noisy", sitesCardName, "want none"},
		{"sites-noisy", sitesLorebookName, "want none"},
		{"sites-lax-data", sitesBadDataName, "got success"},
		{"sites-claims", sitesForeignName, "want null"},
		{"sites-lax-params", sitesBadParamName, "got success"},
		{"sites-bad-source", sitesValidName, "https"},
	}
	for _, tt := range tests {
		t.Run(tt.mode+"/"+tt.check, func(t *testing.T) {
			results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"sites"}, "-mode="+tt.mode), Options{}, quiet)
			if err != nil {
				t.Fatal(err)
			}
			if got := failed(results)[tt.check]; got == nil || !strings.Contains(got.Error(), tt.wantErr) {
				t.Fatalf("check %q: got %v, want %q", tt.check, got, tt.wantErr)
			}
		})
	}
	results, err = Check(context.Background(), testutil.FakePluginWith(t, []string{"sites"}, "-mode=sites-noisy"), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed(results)[sitesValidName]; err != nil {
		t.Fatalf("a valid unwanted source failed the validity check: %v", err)
	}
}

func TestPullOnlyTargetChecks(t *testing.T) {
	// bad-list lists an item without a remote_id, which fails the list check if it runs.
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"pull"}, "-mode=bad-list"), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(results))
	for i, r := range results {
		names[i] = r.Name
	}
	if slices.Contains(names, checkListsName) || !slices.Contains(names, checkGoneName) {
		t.Fatalf("ran %v, want the gone check and no list check", names)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}

	opts := Options{Config: map[string]any{"fail_get": "retryable"}}
	results, err = Check(context.Background(), testutil.FakePluginWith(t, []string{"pull"}), opts, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if got := failed(results)[checkGoneName]; got == nil || !strings.Contains(got.Error(), "-32004") {
		t.Fatalf("a retryable error for a gone item: got %v, want an -32004 failure", got)
	}
}

const checkDeleteName = "a target that claims delete answers target.delete"

func TestDeleteCheckFailsATargetWithoutTheMethod(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"push", "pull"}), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}
	results, err = Check(context.Background(), testutil.FakePluginWith(t, []string{"push", "pull"}, "-mode=no-delete"), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if got := failed(results)[checkDeleteName]; got == nil || !strings.Contains(got.Error(), "-32601") {
		t.Fatalf("a target claiming delete without target.delete: got %v, want a -32601 failure", got)
	}
}

func TestGoneCheckSkipsGetLorebookWithoutStandaloneLorebooks(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"pull"}), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if got := failed(results)[checkGoneName]; got != nil {
		t.Fatalf("an embedded-lorebooks target failed the gone check: %v", got)
	}
}

func TestActionPluginPasses(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"action"}), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}
	if !slices.ContainsFunc(results, func(r Result) bool { return r.Name == checkActionRunsName }) {
		t.Fatalf("no action run check in %v", results)
	}
}

func TestBadActionsFail(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"action"}, "-mode=bad-actions"), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed(results)[checkActionListName]; err == nil || !strings.Contains(err.Error(), "Bad ID") ||
		!strings.Contains(err.Error(), "Bad Skill") {
		t.Fatalf("got %v, want the bad ID and skill named", err)
	}
}

func TestRecordsPluginPasses(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"records"}), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}
	for _, name := range []string{checkRecordsListName, checkRecordsRoundTripName, checkRecordsGoneName, checkRecordsImportName} {
		if !slices.ContainsFunc(results, func(r Result) bool { return r.Name == name }) {
			t.Errorf("no %q check in %v", name, results)
		}
	}
}

func TestBadRecordsFail(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"records"}, "-mode=bad-records"), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed(results)[checkRecordsListName]; err == nil || !strings.Contains(err.Error(), "record id") {
		t.Fatalf("got %v, want the bad record named", err)
	}
}

func TestBadRecordsImportFails(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"records"}, "-mode=bad-records-import"), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed(results)[checkRecordsImportName]; err == nil || !strings.Contains(err.Error(), "record id") {
		t.Fatalf("got %v, want the bad record named", err)
	}
}

func TestEnrichPluginPasses(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"enrich"}), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}
	if !slices.ContainsFunc(results, func(r Result) bool { return r.Name == checkEnrichName }) {
		t.Errorf("no enrich check in %v", results)
	}
}

func TestBadEnrichFails(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"enrich"}, "-mode=bad-enrich"), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed(results)[checkEnrichName]; err == nil || !strings.Contains(err.Error(), "mood isn't declared") {
		t.Fatalf("got %v, want the undeclared key named", err)
	}
}

func TestAIPluginAnswersItsServices(t *testing.T) {
	for services, wantPass := range map[string]bool{"ai.chat": true, "ai.embed": true, "ai.decide": true} {
		opts := Options{Config: map[string]any{"services": []any{services}}}
		results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"ai"}), opts, quiet)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(results, func(r Result) bool { return r.Name == checkServicesName })
		if i < 0 {
			t.Fatalf("%s: the ai check didn't run: %v", services, results)
		}
		if (results[i].Err == nil) != wantPass {
			t.Errorf("%s: got %v, want pass %v", services, results[i].Err, wantPass)
		}
	}
}

func TestAIPluginWithAnEmptyDecisionFails(t *testing.T) {
	opts := Options{Config: map[string]any{"services": []any{"ai.decide"}}}
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"ai"}, "-mode=bad-decide"), opts, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed(results)[checkServicesName]; err == nil || !strings.Contains(err.Error(), "has no answer") {
		t.Fatalf("got %v, want the unanswered question named", err)
	}
}

// provide makes the fake an ai.chat provider that streams from the check's
// model server.
func provide(t *testing.T) Options {
	return Options{Provider: testutil.FakePluginWith(t, []string{"ai"}),
		ProviderConfig: `{"services": ["ai.chat"], "base_url": "${INNKEEPER_FAKE_AI_URL}"}`}
}

func TestConsumerCallsAProviderThroughTheBroker(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakeConsumer(t, []string{"action"}), provide(t), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}
	for _, name := range []string{checkProviderName, checkReplyName, checkDeltasName, checkToolsName, checkCancelName} {
		if !slices.ContainsFunc(results, func(r Result) bool { return r.Name == name }) {
			t.Errorf("check %q didn't run", name)
		}
	}
}

func TestConsumerCallsAProviderFromEachDrivenCapability(t *testing.T) {
	opts := provide(t)
	for _, capability := range []string{"mcp", "import", "records", "settings"} {
		t.Run(capability, func(t *testing.T) {
			results, err := Check(context.Background(), testutil.FakeConsumer(t, []string{capability}), opts, quiet)
			if err != nil {
				t.Fatal(err)
			}
			if f := failed(results); len(f) > 0 {
				t.Fatalf("checks failed: %v", f)
			}
		})
	}
}

func TestConsumerThatDropsTheToolCallFails(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakeConsumer(t, []string{"action"}, "-mode=drop-tool-call"), provide(t), quiet)
	if err != nil {
		t.Fatal(err)
	}
	err = failed(results)[checkToolsName]
	if err == nil || !strings.Contains(err.Error(), "without the assistant message") {
		t.Fatalf("got %v, want the missing call named", err)
	}
}

func TestUsesWithoutAProviderFails(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakeConsumer(t, []string{"action"}), Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	err = failed(results)[checkProviderName]
	if err == nil || !strings.Contains(err.Error(), "INNKEEPER_PROVIDER_PLUGIN") {
		t.Fatalf("got %v, want INNKEEPER_PROVIDER_PLUGIN named", err)
	}
}
