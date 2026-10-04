package library

import (
	"context"
	"encoding/json"
	"slices"
)

// ExportCard returns a card's head revision as a CCv3 card, with its linked
// lorebooks embedded as data.character_book.
func (l *Library) ExportCard(ctx context.Context, id int64) (json.RawMessage, error) {
	c, err := l.Cards.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	rev, err := l.Cards.Revision(ctx, c.HeadRevisionID)
	if err != nil {
		return nil, err
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(rev.Data, &data); err != nil {
		return nil, err
	}
	book, _, err := l.CardBook(ctx, id)
	if err != nil {
		return nil, err
	}
	if book != nil {
		data["character_book"] = book
	}
	return json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data})
}

// CardBook returns the character_book a card embeds: the first linked
// lorebook's fields, with every linked lorebook's entries. The book is nil
// when no lorebook is linked. It also returns the head revision ID of each
// linked lorebook, sorted.
//
// ponytail: merged entries keep their own id fields, which can repeat across
// lorebooks; renumber them if an app minds.
func (l *Library) CardBook(ctx context.Context, cardID int64) (json.RawMessage, []int64, error) {
	links, err := l.CardLorebooks(ctx, cardID)
	if err != nil {
		return nil, nil, err
	}
	var book map[string]any
	entries := []any{}
	revs := []int64{}
	for _, lid := range links {
		lb, err := l.Lorebooks.Get(ctx, lid)
		if err != nil {
			return nil, nil, err
		}
		lrev, err := l.Lorebooks.Revision(ctx, lb.HeadRevisionID)
		if err != nil {
			return nil, nil, err
		}
		revs = append(revs, lrev.ID)
		var b map[string]any
		if err := json.Unmarshal(lrev.Data, &b); err != nil {
			return nil, nil, err
		}
		if book == nil {
			book = b
		}
		if e, ok := b["entries"].([]any); ok {
			entries = append(entries, e...)
		}
	}
	slices.Sort(revs)
	if book == nil {
		return nil, revs, nil
	}
	book["entries"] = entries
	raw, err := json.Marshal(book)
	return raw, revs, err
}
