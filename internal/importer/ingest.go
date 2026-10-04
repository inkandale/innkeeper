package importer

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/protocol"
)

// badItem is an emitted item the plugin got wrong. It is counted as failed
// and the import goes on.
type badItem struct{ err error }

func (b *badItem) Error() string { return b.err.Error() }
func (b *badItem) Unwrap() error { return b.err }

// invalid turns the library's validation errors into badItem.
func invalid(err error) error {
	if errors.Is(err, library.ErrInvalid) {
		return &badItem{err}
	}
	return err
}

// ingestCard stores an emitted card. An embedded character_book becomes a
// linked lorebook. It returns whether the card is new.
func (im *Importer) ingestCard(ctx context.Context, plugin string, e protocol.EmitCard) (bool, error) {
	if e.Item == "" {
		return false, &badItem{errors.New("item is empty")}
	}
	var card struct {
		Spec string                     `json:"spec"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(e.Card, &card); err != nil {
		return false, &badItem{err}
	}
	if card.Spec != "chara_card_v3" {
		return false, &badItem{fmt.Errorf("spec %q is not chara_card_v3", card.Spec)}
	}
	if card.Data == nil {
		return false, &badItem{errors.New("card has no data")}
	}
	var name string
	_ = json.Unmarshal(card.Data["name"], &name)
	book := card.Data["character_book"]
	delete(card.Data, "character_book")
	data, err := json.Marshal(card.Data)
	if err != nil {
		return false, err
	}
	provenance, source := plugin+":"+e.Item, "plugin:"+plugin

	var avatar string
	if e.Avatar != "" {
		b, err := im.Lib.IngestBlob(ctx, e.Avatar)
		if err != nil {
			return false, &badItem{fmt.Errorf("avatar: %w", err)}
		}
		avatar = b.SHA256
	}
	// The lorebook goes first, and linking is idempotent, so a run
	// interrupted partway leaves nothing a retry can't finish.
	var lorebookID int64
	if len(book) > 0 && string(book) != "null" {
		var meta struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(book, &meta)
		lorebookID, _, err = im.Lib.Lorebooks.Import(ctx, provenance, cmp.Or(meta.Name, name+" lorebook"),
			library.Edit{Data: book, Source: source, Message: "Split from imported card"})
		if err != nil {
			return false, invalid(err)
		}
	}
	cardID, created, err := im.Lib.Cards.Import(ctx, provenance, name,
		library.Edit{Data: data, AvatarBlob: avatar, Source: source, Message: "Imported"})
	if err != nil {
		return false, invalid(err)
	}
	if lorebookID != 0 {
		if err := im.Lib.LinkLorebook(ctx, cardID, lorebookID); err != nil {
			return false, err
		}
	}
	return created, nil
}

// ingestLorebook stores an emitted standalone lorebook. It returns whether
// the lorebook is new.
func (im *Importer) ingestLorebook(ctx context.Context, plugin string, e protocol.EmitLorebook) (bool, error) {
	if e.Item == "" {
		return false, &badItem{errors.New("item is empty")}
	}
	var meta struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(e.Lorebook, &meta); err != nil {
		return false, &badItem{err}
	}
	_, created, err := im.Lib.Lorebooks.Import(ctx, plugin+":"+e.Item, cmp.Or(meta.Name, "lorebook"),
		library.Edit{Data: e.Lorebook, Source: "plugin:" + plugin, Message: "Imported"})
	return created, invalid(err)
}
