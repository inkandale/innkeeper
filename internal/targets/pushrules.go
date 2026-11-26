package targets

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/inkandale/innkeeper/internal/library"
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
		_, err := s.push(ctx, cardKind, app.InstanceID, id, false, rand.Text(), nil)
		if errors.Is(err, ErrBlocked) {
			app.Totals["rule_blocked"]++
			continue
		}
		if err != nil {
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
	rules, err := s.lib.RulesTo(ctx, library.RulePush, instance)
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

// checkBlocked returns ErrBlocked, naming the rule, if an enabled block rule
// to instance matches card.
func (s *Service) checkBlocked(ctx context.Context, instance, card int64) error {
	by, err := s.blockedBy(ctx, instance, []int64{card})
	if err != nil || by[card] == "" {
		return err
	}
	return fmt.Errorf("%w: rule %q", ErrBlocked, by[card])
}

// Blocked returns, in ID order, the cards an enabled block rule keeps off
// instance.
func (s *Service) Blocked(ctx context.Context, instance int64, cards []int64) ([]int64, error) {
	by, err := s.blockedBy(ctx, instance, cards)
	return slices.Sorted(maps.Keys(by)), err
}

// blockedBy maps each of cards an enabled block rule to instance matches to
// the first such rule's name.
func (s *Service) blockedBy(ctx context.Context, instance int64, cards []int64) (map[int64]string, error) {
	by := map[int64]string{}
	rules, err := s.lib.RulesTo(ctx, library.RuleBlock, instance)
	if err != nil || len(rules) == 0 || len(cards) == 0 {
		return by, err
	}
	libOf, err := s.lib.CardLibraries(ctx, cards)
	if err != nil {
		return nil, err
	}
	inLib := map[int64][]int64{}
	for card, lib := range libOf {
		inLib[lib] = append(inLib[lib], card)
	}
	browsable, err := libsettings.New(s.lib, s.reg).Browsable(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		for lib, among := range inLib {
			if !r.Covers(lib) {
				continue
			}
			m, err := s.lib.RuleMatches(ctx, lib, r.Filter, browsable, among)
			if err != nil {
				return nil, fmt.Errorf("rule %q: %w", r.Name, err)
			}
			for _, id := range m {
				if by[id] == "" {
					by[id] = r.Name
				}
			}
		}
	}
	return by, nil
}
