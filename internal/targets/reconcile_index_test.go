package targets

import (
	"os"
	"testing"

	"github.com/inkandale/innkeeper/cardhash"
	"github.com/inkandale/innkeeper/internal/library"
)

func TestLoadIndex(t *testing.T) {
	e := setup(t, nil)
	other := e.newCard("Iko")
	f, err := os.Open(pngFile(t))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	avatar, err := e.lib.PutBlob(e.ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	e.update(other, func(ed *library.Edit) {
		ed.AvatarBlob = avatar.SHA256
		ed.Overrides = []byte(`{"` + e.key() + `": {"description": "An elf courier."}}`)
	})
	bound := e.push(e.card)

	inst, info, err := e.s.describe(e.ctx, e.inst)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := e.s.loadIndex(e.ctx, cardKind, inst, info)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.entries) != 2 {
		t.Fatalf("entries = %+v", idx.entries)
	}
	for _, ent := range idx.entries {
		r, err := e.s.render(e.ctx, cardKind, e.inst, ent.id, info)
		if err != nil {
			t.Fatal(err)
		}
		projected, hash, err := Project(r.data, info.Fields)
		if err != nil {
			t.Fatal(err)
		}
		if ent.hash != hash {
			t.Errorf("card %d hash = %s, want %s", ent.id, ent.hash, hash)
		}
		if sim, _, _ := cardhash.CardSimhash(projected); ent.simhash == nil || *ent.simhash != sim {
			t.Errorf("card %d simhash = %v, want %d", ent.id, ent.simhash, sim)
		}
		if ent.id == other {
			if ent.phash == nil || avatar.AvatarPhash == nil || *ent.phash != *avatar.AvatarPhash {
				t.Errorf("phash = %v, want %v", ent.phash, avatar.AvatarPhash)
			}
			if ent.name != "Iko" || ent.norm != "iko" {
				t.Errorf("name = %q, %q", ent.name, ent.norm)
			}
			// The override reaches the projected hash.
			if _, base, _ := Project(e.head(other).Data, info.Fields); ent.hash == base {
				t.Error("hash ignores the override")
			}
			if *ent.simhash == *e.head(other).TextSimhash {
				t.Error("simhash ignores the override")
			}
		} else if ent.phash != nil {
			t.Errorf("card %d phash = %v", ent.id, *ent.phash)
		}
	}
	b, ok := idx.byRemote[bound.RemoteID]
	if !ok || b.id != bound.ID || b.entityID != e.card || len(b.accepted) == 0 {
		t.Errorf("byRemote = %+v", idx.byRemote)
	}
	if idx.remoteOf[e.card] != bound.RemoteID || len(idx.remoteOf) != 1 {
		t.Errorf("remoteOf = %v", idx.remoteOf)
	}
}

func TestNormName(t *testing.T) {
	if got := normName("  Brakka\tthe   SMITH \n"); got != "brakka the smith" {
		t.Errorf("normName = %q", got)
	}
}
