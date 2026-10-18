package services

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fixture struct {
	reg                *plugins.Registry
	mgr                *pluginhost.Manager
	chat, embed        pluginhost.Instance
	consumer, stranger pluginhost.Instance
	chatID, embedID    int64
	strangerID         int64
}

// as is the fake's manifest under another name, with one capability and uses.
func as(m *protocol.Manifest, name, capability string, uses ...string) *protocol.Manifest {
	c := *m
	c.Name, c.Capabilities, c.Uses = name, []string{capability}, uses
	c.Command = []string{"./fakeplugin", "-caps=" + capability, "-name=" + name}
	return &c
}

func setup(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"ai"}))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := plugins.New(testdb.New(t), map[string]*protocol.Manifest{
		"fake":     m,
		"consumer": as(m, "consumer", "action", protocol.MethodAIChat),
		"stranger": as(m, "stranger", "action"),
	}, &blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	runs := pluginhost.NewRuns(quiet)
	b := &Broker{Registry: reg, Runs: runs}
	mgr := pluginhost.NewManager(b.Handle(runs.Handle), time.Minute, quiet)
	b.Plugins = mgr
	t.Cleanup(mgr.Close)
	create := func(plugin, name string, config map[string]any) (int64, pluginhost.Instance) {
		id, err := reg.CreateInstance(ctx, plugins.NewInstance{Plugin: plugin, Name: new(name), Config: config})
		if err != nil {
			t.Fatal(err)
		}
		inst, err := reg.Instance(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return id, inst
	}
	f := fixture{reg: reg, mgr: mgr}
	f.chatID, f.chat = create("fake", "Chat", map[string]any{"services": []string{protocol.MethodAIChat}})
	f.embedID, f.embed = create("fake", "Embed", map[string]any{"services": []string{protocol.MethodAIEmbed}})
	_, f.consumer = create("consumer", "Consumer", nil)
	f.strangerID, f.stranger = create("stranger", "Stranger", nil)
	return f
}

type relayed struct {
	Result protocol.AIChatResult `json:"result"`
	Deltas []string              `json:"deltas"`
}

// relayChat has from's plugin send ai.chat for text to the instance to.
func (f fixture) relayChat(ctx context.Context, from pluginhost.Instance, to int64, text string) (relayed, error) {
	params := protocol.AIChatParams{ServiceCall: protocol.ServiceCall{Instance: to, Run: "consumer-run"},
		Messages: []protocol.ChatMessage{{Role: "user", Content: text}}}
	var out relayed
	err := f.mgr.Call(ctx, from, "relay", map[string]any{"method": protocol.MethodAIChat, "params": params}, &out)
	return out, err
}

func wantForbidden(t *testing.T, err error) {
	t.Helper()
	var rpcErr *protocol.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != protocol.CodeForbidden {
		t.Fatalf("got %v, want -32003", err)
	}
}

func TestChatReachesTheProviderAndStreamsBack(t *testing.T) {
	f := setup(t)
	got, err := f.relayChat(context.Background(), f.consumer, f.chatID, "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if got.Result.Text != "echo: hello there" || !slices.Equal(got.Deltas, []string{"hello ", "there "}) {
		t.Fatalf("relayed %+v", got)
	}
}

func TestCallerWithoutUsesIsRefused(t *testing.T) {
	f := setup(t)
	_, err := f.relayChat(context.Background(), f.stranger, f.chatID, "hello")
	wantForbidden(t, err)
}

func TestProviderThatDoesntOfferTheServiceIsRefused(t *testing.T) {
	f := setup(t)
	_, err := f.relayChat(context.Background(), f.consumer, f.embedID, "hello")
	wantForbidden(t, err)
}

func TestProviderWithoutTheCapabilityIsRefused(t *testing.T) {
	f := setup(t)
	_, err := f.relayChat(context.Background(), f.consumer, f.strangerID, "hello")
	wantForbidden(t, err)
}

func TestMissingProviderIsRefused(t *testing.T) {
	f := setup(t)
	_, err := f.relayChat(context.Background(), f.consumer, f.chatID+1000, "hello")
	wantForbidden(t, err)
}

func TestBrokenProviderIsRefusedWithoutItsError(t *testing.T) {
	f := setup(t)
	if err := f.reg.MarkBroken(context.Background(), f.chatID, f.chat.Version, errors.New("secret-laden failure")); err != nil {
		t.Fatal(err)
	}
	_, err := f.relayChat(context.Background(), f.consumer, f.chatID, "hello")
	wantForbidden(t, err)
	if strings.Contains(err.Error(), "secret-laden") {
		t.Fatalf("refusal leaks the broken error: %v", err)
	}
}

func TestServiceCallDuringInitializeIsRefused(t *testing.T) {
	f := setup(t)
	id, err := f.reg.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "consumer", Name: new("Eager"),
		Config: map[string]any{"chat_on_initialize": f.chatID}})
	if err != nil {
		t.Fatal(err)
	}
	eager, err := f.reg.Instance(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var code int
	if err := f.mgr.Call(ctx, eager, "initialize-chat", nil, &code); err != nil {
		t.Fatal(err)
	}
	if code != protocol.CodeForbidden {
		t.Fatalf("ai.chat during initialize got code %d, want -32003", code)
	}
}

func TestCancellingTheCallerCancelsTheProvider(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := f.relayChat(ctx, f.consumer, f.chatID, "hang")
		done <- err
	}()
	count := func(method string) int {
		var n int
		if err := f.mgr.Call(context.Background(), f.chat, "calls", method, &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	eventually(t, func() bool { return count(protocol.MethodAIChat) == 1 })
	cancel()
	<-done
	eventually(t, func() bool { return count("ai.chat.cancelled") == 1 })
}

func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("condition never held")
}
