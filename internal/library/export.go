package library

import (
	"context"
	"encoding/json"
)

// ExportCard returns a card's head revision as a CCv3 card. Its linked
// lorebooks become data.character_book: the first one's fields, with every
// linked lorebook's entries.
//
// ponytail: merged entries keep their own id fields, which can repeat across
// lorebooks; renumber them if an app minds.
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
	links, err := l.CardLorebooks(ctx, id)
	if err != nil {
		return nil, err
	}
	var book map[string]any
	entries := []any{}
	for _, lid := range links {
		lb, err := l.Lorebooks.Get(ctx, lid)
		if err != nil {
			return nil, err
		}
		lrev, err := l.Lorebooks.Revision(ctx, lb.HeadRevisionID)
		if err != nil {
			return nil, err
		}
		var b map[string]any
		if err := json.Unmarshal(lrev.Data, &b); err != nil {
			return nil, err
		}
		if book == nil {
			book = b
		}
		if e, ok := b["entries"].([]any); ok {
			entries = append(entries, e...)
		}
	}
	if book != nil {
		book["entries"] = entries
		if data["character_book"], err = json.Marshal(book); err != nil {
			return nil, err
		}
	}
	return json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data})
}
