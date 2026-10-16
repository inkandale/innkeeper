package plugins

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

func TestInstancesOfferingListsOnlyInstancesThatOfferTheService(t *testing.T) {
	ctx := context.Background()
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"ai"}))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{"fake": m},
		&blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	mgr := pluginhost.NewManager(nil, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	create := func(name string, services ...string) int64 {
		id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: name,
			Config: map[string]any{"services": services}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	chat := create("Chat", protocol.MethodAIChat)
	create("Embed", protocol.MethodAIEmbed)

	got, err := reg.InstancesOffering(ctx, mgr, protocol.MethodAIChat)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != chat {
		t.Fatalf("instances offering ai.chat = %+v, want only %d", got, chat)
	}
	if _, err := reg.InstancesOffering(ctx, mgr, "ai.draw"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unknown service: %v, want ErrInvalid", err)
	}
}
