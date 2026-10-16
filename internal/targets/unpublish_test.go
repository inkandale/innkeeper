package targets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
)

func (e *env) inApp(remoteID string) bool {
	_, err := os.Stat(filepath.Join(e.dir, remoteID+".json"))
	return err == nil
}

func TestUnpublishDeletesTheAppCopyAndUnbinds(t *testing.T) {
	e := setup(t, nil)
	e.push(e.card)
	if err := e.s.Unpublish(e.ctx, library.Card, e.card); err != nil {
		t.Fatal(err)
	}
	if e.inApp("r1") {
		t.Fatal("the app still has r1")
	}
	if n := e.bindingCount(); n != 0 {
		t.Fatalf("%d bindings left; want none", n)
	}
}

func TestUnpublishForgetsACopyTheAppAlreadyLost(t *testing.T) {
	e := setup(t, nil)
	e.push(e.card)
	if err := os.Remove(filepath.Join(e.dir, "r1.json")); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Unpublish(e.ctx, library.Card, e.card); err != nil {
		t.Fatal(err)
	}
	if n := e.bindingCount(); n != 0 {
		t.Fatalf("%d bindings left; want none", n)
	}
}

func TestUnpublishKeepsTheCopyInAnAppThatCantDelete(t *testing.T) {
	e := setup(t, map[string]any{"delete": false})
	e.push(e.card)
	if err := e.s.Unpublish(e.ctx, library.Card, e.card); err != nil {
		t.Fatal(err)
	}
	if !e.inApp("r1") || e.bindingCount() != 1 {
		t.Fatal("want the app's copy and its binding kept")
	}
}

func TestUnpublishFailureKeepsTheBinding(t *testing.T) {
	e := setup(t, map[string]any{"fail_delete": true})
	e.push(e.card)
	if err := e.s.Unpublish(e.ctx, library.Card, e.card); err == nil {
		t.Fatal("unpublish worked; want the app's error")
	}
	if !e.inApp("r1") || e.bindingCount() != 1 {
		t.Fatal("want the app's copy and its binding kept")
	}
}

func TestUnpublishDeletesAStandaloneLorebook(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("forge-lore")
	b := e.pushLorebook(lb)
	if err := e.s.Unpublish(e.ctx, library.Lorebook, lb); err != nil {
		t.Fatal(err)
	}
	if e.inApp(b.RemoteID) || e.bindingCount() != 0 {
		t.Fatalf("want %s deleted and unbound", b.RemoteID)
	}
}

func TestUnpublishLeavesAFollowedSourceAlone(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(map[string]any{"delete": true})
	card := e.imported("vyr", vyrCard)
	e.publish("vyr", vyrCard, "")
	e.follow(card)
	if err := e.s.Unpublish(e.ctx, library.Card, card); err != nil {
		t.Fatal(err)
	}
	if e.bindingCount() != 1 {
		t.Fatal("unpublish dropped the source binding")
	}
	if _, err := os.Stat(filepath.Join(e.updir, "fake:vyr.json")); err != nil {
		t.Fatalf("the site lost its page: %v", err)
	}
}
