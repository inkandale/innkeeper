package importer

import (
	"cmp"
	"context"
	"errors"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/protocol"
)

// badItem is an emitted item the plugin got wrong. It is counted as failed
// and the import goes on.
type badItem struct{ err error }

func (b *badItem) Error() string { return b.err.Error() }
func (b *badItem) Unwrap() error { return b.err }

// invalid turns the library's and blob store's validation errors into badItem.
func invalid(err error) error {
	if errors.Is(err, library.ErrInvalid) || errors.Is(err, blobstore.ErrInvalid) {
		return &badItem{err}
	}
	return err
}

// ingestCard stores an emitted card. It returns whether the card is new.
func (im *Importer) ingestCard(ctx context.Context, plugin string, e protocol.EmitCard) (bool, error) {
	if e.Item == "" {
		return false, &badItem{errors.New("item is empty")}
	}
	_, created, err := im.Lib.ImportCard(ctx, plugin+":"+e.Item, "plugin:"+plugin, e.Card, e.Avatar)
	return created, invalid(err)
}

// ingestLorebook stores an emitted standalone lorebook. It returns whether
// the lorebook is new.
func (im *Importer) ingestLorebook(ctx context.Context, plugin string, e protocol.EmitLorebook) (bool, error) {
	if e.Item == "" {
		return false, &badItem{errors.New("item is empty")}
	}
	_, created, err := im.Lib.Lorebooks.Import(ctx, plugin+":"+e.Item, cmp.Or(library.DataName(e.Lorebook), "lorebook"),
		library.Edit{Data: e.Lorebook, Source: "plugin:" + plugin, Message: "Imported"})
	return created, invalid(err)
}
