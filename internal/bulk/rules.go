package bulk

import (
	"context"
	"fmt"
	"maps"

	"github.com/inkandale/innkeeper/internal/library"
)

// ApplyRules applies the rules the hide job applies in libraryID to its
// cards: all of them, or only those in among unless among is nil. hidden
// is the duplicates to archive, card ID to reason. Each archive rule's
// matches join it, a card keeping the first reason it gets, so a duplicate
// stays a duplicate and the lowest rule ID wins, and SetHidden applies the
// list. browsable, as Query.Browsable, resolves enrichment terms that name no
// instance. It adds what it did to prog.
func ApplyRules(ctx context.Context, lib *library.Library, libraryID int64, browsable map[string][]library.Scorer,
	hidden map[int64]string, among []int64, prog *HideProgress) error {
	rules, err := lib.RulesIn(ctx, libraryID)
	if err != nil {
		return err
	}
	hidden = maps.Clone(hidden)
	if hidden == nil {
		hidden = map[int64]string{}
	}
	for _, r := range rules {
		var matches []int64
		if r.Covers(libraryID) {
			if matches, err = lib.RuleMatches(ctx, libraryID, r.Filter, browsable, among); err != nil {
				return fmt.Errorf("rule %q: %w", r.Name, err)
			}
		}
		if r.Action == library.RuleArchive {
			for _, id := range matches {
				if _, ok := hidden[id]; !ok {
					hidden[id] = r.Reason()
				}
			}
			continue
		}
		add, remove, err := lib.RuleChanges(ctx, r, libraryID, matches, among)
		if err != nil {
			return fmt.Errorf("rule %q: %w", r.Name, err)
		}
		if err := lib.CollectByRule(ctx, r, add, false); err != nil {
			return fmt.Errorf("rule %q: %w", r.Name, err)
		}
		if err := lib.CollectByRule(ctx, r, remove, true); err != nil {
			return fmt.Errorf("rule %q: %w", r.Name, err)
		}
		prog.Collected += len(add)
		prog.Uncollected += len(remove)
	}
	archived, unarchived, err := lib.SetHidden(ctx, libraryID, hidden, among)
	prog.Archived += archived
	prog.Unarchived += unarchived
	return err
}
