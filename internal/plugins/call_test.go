package plugins

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

func TestCallMarksBrokenOnStartFailure(t *testing.T) {
	ctx := context.Background()
	m, err := protocol.LoadManifest(testutil.FakePlugin(t, "-mode=wrong-version"))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{"fake": m}, store, box)
	mgr := pluginhost.NewManager(nil, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake")})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var startErr *pluginhost.StartError
	if err := reg.Call(ctx, mgr, inst, protocol.MethodTargetDescribe, nil, nil); !errors.As(err, &startErr) {
		t.Fatalf("call: %v, want a start error", err)
	}
	if _, err := reg.Instance(ctx, id); !errors.Is(err, ErrBroken) {
		t.Fatalf("instance after the failed start: %v, want ErrBroken", err)
	}
}

func TestRetryable(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{protocol.ErrClosed, true},
		{fmt.Errorf("put: %w", protocol.NewError(-32000, "rate limited", true)), true},
		{protocol.NewError(-32000, "rejected", false), false},
		{errors.New("database down"), false},
	}
	for _, tt := range tests {
		if got := Retryable(tt.err); got != tt.want {
			t.Errorf("Retryable(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestJobErrRetriesOnlyWhatMayWorkLater(t *testing.T) {
	tests := []struct {
		err   error
		retry bool
	}{
		{protocol.ErrClosed, true},
		{&pluginhost.StartError{Err: protocol.ErrClosed}, false},
		{protocol.NewError(-32000, "rejected", false), false},
	}
	for _, tt := range tests {
		got := JobErr(tt.err)
		if errors.Is(got, jobs.ErrRetry) != tt.retry || !errors.Is(got, tt.err) {
			t.Errorf("JobErr(%v) = %v, want retry %v", tt.err, got, tt.retry)
		}
	}
}

func TestHasCapability(t *testing.T) {
	reg := New(nil, map[string]*protocol.Manifest{"fake": {Capabilities: []string{"push"}}}, nil, nil)
	if !reg.HasCapability("fake", "push") || reg.HasCapability("fake", "pull") || reg.HasCapability("missing", "push") {
		t.Fatal("HasCapability is wrong")
	}
}

func TestCheckRefusesDisabledInstance(t *testing.T) {
	ctx := context.Background()
	m, err := protocol.LoadManifest(testutil.FakePlugin(t, "-mode=wrong-version"))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{"fake": m}, store, box)
	mgr := pluginhost.NewManager(nil, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Fake")})
	if err != nil {
		t.Fatal(err)
	}
	disabled := true
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if problem, err := reg.Check(ctx, mgr, id); !errors.Is(err, ErrDisabled) || problem != nil {
		t.Fatalf("Check: problem = %v, err = %v, want ErrDisabled", problem, err)
	}
	got, err := reg.GetInstance(ctx, id)
	if err != nil || got.BrokenError != nil {
		t.Fatalf("after Check: broken = %v, err = %v; the plugin must not have started", got.BrokenError, err)
	}
}
