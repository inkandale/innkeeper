package contract

import (
	"context"
	"io"
	"log/slog"
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

func TestWellBehavedPluginPasses(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePlugin(t), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Fatalf("ran %d checks, want 5", len(results))
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
