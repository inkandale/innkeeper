package export

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/testdb"
)

func TestCardJSON(t *testing.T) {
	ctx := context.Background()
	lib := library.New(testdb.New(t), &blobstore.Store{Name: "library", Root: t.TempDir()})
	c, _, err := lib.Cards.Create(ctx, library.DefaultLibrary, "brakka", "upload", library.Edit{Data: []byte(`{"name": "Brakka"}`), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := (&Exporter{Lib: lib}).Card(ctx, c.ID, "json")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if f.Name != "brakka.json" || f.Type != "application/json" || json.Unmarshal(f.Body, &card) != nil || card.Data.Name != "Brakka" {
		t.Fatalf("file = %s %s %s", f.Name, f.Type, f.Body)
	}
}
