package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/protocol"
)

func TestQueryBatchesStopAtTheCap(t *testing.T) {
	ctx := context.Background()
	lib := library.New(testdb.New(t), &blobstore.Store{Name: "library", Root: t.TempDir()})
	for i := range protocol.MaxEnrichBatch + 5 {
		if _, _, err := lib.Cards.Create(ctx, library.DefaultLibrary, fmt.Sprintf("c-%d", i), "upload",
			library.Edit{Data: json.RawMessage(`{"name": "C"}`), Source: "ui"}); err != nil {
			t.Fatal(err)
		}
	}
	max := protocol.MaxEnrichBatch + 2
	next, total, err := Payload{Query: &library.Query{}}.batches(ctx, lib, max)
	if err != nil || total != max {
		t.Fatalf("total = %d, %v; want %d", total, err, max)
	}
	read := 0
	for {
		ids, err := next()
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 {
			break
		}
		read += len(ids)
	}
	if read != max {
		t.Fatalf("read %d cards, want %d", read, max)
	}
}
