package importer

import (
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
