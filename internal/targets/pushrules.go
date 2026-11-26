package targets

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"

	"github.com/inkandale/innkeeper/internal/libsettings"
)

// pushRules pushes the cards the app's enabled push rules match and the app
// has no copy of, creating one, except those in review, which an app item
// waiting for the owner may already be a copy of. It returns how many it
// pushed and the unarchived matches, for autosync to keep up to date.
//
// ponytail: cards are matched once per sync, not when a card changes; queue
// a sync from card edits if the sync interval proves too slow.
func (s *Service) pushRules(ctx context.Context, app *SyncApp, review []int64) (matched []int64, cards int) {
	failed := func(card int64, err error) {
		app.Totals["rule_failed"]++
		s.log.Warn("sync: push rule failed", "instance", app.InstanceID, "card", card, "err", err)
	}
	create, matched, err := s.pushRuleCards(ctx, app.InstanceID)
	if err != nil {
		failed(0, err)
		return matched, 0
	}
	for _, id := range create {
		if ctx.Err() != nil {
			return matched, cards
		}
		if slices.Contains(review, id) {
			app.Totals["rule_review"]++
			continue
		}
		if _, err := s.push(ctx, cardKind, app.InstanceID, id, false, rand.Text(), nil); err != nil {
			failed(id, err)
			continue
		}
		app.Totals["rule_pushed"]++
		cards++
	}
	return matched, cards
}

// pushRuleCards returns the cards instance's push rules would create there,
// and every unarchived card they match.
func (s *Service) pushRuleCards(ctx context.Context, instance int64) (create, matched []int64, err error) {
	rules, err := s.lib.PushRules(ctx, instance)
	if err != nil || len(rules) == 0 {
		return nil, nil, err
	}
	browsable, err := libsettings.New(s.lib, s.reg).Browsable(ctx)
	if err != nil {
		return nil, nil, err
	}
	seen := map[int64]bool{}
	for _, r := range rules {
		libs, err := s.lib.RuleScope(ctx, r)
		if err != nil {
			return nil, nil, fmt.Errorf("rule %q: %w", r.Name, err)
		}
		for _, lib := range libs {
			m, err := s.lib.RuleMatches(ctx, lib, r.Filter, browsable, nil)
			if err != nil {
				return nil, nil, fmt.Errorf("rule %q: %w", r.Name, err)
			}
			add, _, err := s.lib.RuleChanges(ctx, r, lib, m, nil)
			if err != nil {
				return nil, nil, fmt.Errorf("rule %q: %w", r.Name, err)
			}
			for _, id := range add {
				if !seen[id] {
					seen[id] = true
					create = append(create, id)
				}
			}
			if m, err = s.lib.Unarchived(ctx, m); err != nil {
				return nil, nil, err
			}
			matched = append(matched, m...)
		}
	}
	return create, matched, nil
}

// reviewCandidates returns the cards that snap's unbound app items may be
// copies of, which the owner has yet to resolve.
func reviewCandidates(snap Snapshot) []int64 {
	var ids []int64
	for _, it := range snap.Items {
		if it.BindingID != 0 || it.AutoBound || it.AutoImported {
			continue
		}
		for _, c := range it.Candidates {
			ids = append(ids, c.CardID)
		}
	}
	return ids
}
