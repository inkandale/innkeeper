package importer

import (
	"context"
	"errors"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/library"
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

// ingest stores an item the plugin emitted under its ID for the item. It
// returns whether the item is new.
func (im *Importer) ingest(ctx context.Context, plugin, item string, it library.PluginItem) (bool, error) {
	if item == "" {
		return false, &badItem{errors.New("item is empty")}
	}
	it.Provenance, it.Source = plugin+":"+item, "plugin:"+plugin
	got, err := im.Lib.ImportItem(ctx, it)
	return got.Outcome == library.Created, invalid(err)
}
