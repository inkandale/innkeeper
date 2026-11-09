# Rule wizard

Add and edit rules from the Rules page in four steps, building the rule's
search there instead of on the Cards page first. Builds on
[rules](2026-11-06-rules-design.md).

## Decisions

| Question | Decision |
|---|---|
| Default rules | None. Plugins' shipped saved searches stay as they are: created with a new instance, and one choice among others in step 1. |
| Where rules start | Add rule on the Rules page opens the wizard at `/rules/new`. Add rule on a saved search opens it with that search picked. |
| Building a search | Step 1 embeds the Cards page's filter sidebar (`Facets`) on local state, not the URL. Search text isn't offered, since a saved search can't hold it. |
| Saving a built search | `POST /rules` takes `filter` in place of `saved_search_id`, and core saves a search under the rule's name in the same transaction. A taken name fails with 409 and nothing is saved. |
| Previewing a built search | `POST /rules/preview` takes `filter` the same way and saves nothing. |
| Editing | `/rules/:id` opens the same wizard at step 4, every step reachable. Step 1 shows the rule's saved search and can switch to another; changing a search's filter stays on the Cards page (Update). Action and collection stay fixed. |
| Counts while building | Step 1 shows how many cards match in the current library, as the sidebar's counts do. Step 4's preview covers every library the rule would. |

## Steps

1. **Match.** Two choices:
   - **Build**, the default when there are no saved searches: the filter
     sidebar, the terms it picked as removable chips, and the match count.
   - **Saved search**: a list of saved searches.

   Next needs at least one term, or a picked search.
2. **Do.** Archive, or add to a collection, with its library and collection.
3. **Where.** All libraries, new ones included, or the ones picked. A
   collect rule covers its collection's library alone, so this step is
   skipped.
4. **Review.** Name (required), a summary of steps 1-3 with links back, and
   the preview. Save adds the rule off; Save and turn on adds it on after
   the preview is confirmed, as turning a rule on does today.

## API

`RuleSpec` gains `filter`, a `Filter`, used only when `saved_search_id` is
0. Exactly one of them is set, or the request fails with 422.

- `POST /rules` with `filter` saves a search named after the rule, with no
  sort, then the rule on it, in one transaction.
- `POST /rules/preview` with `filter` previews it unsaved.
- `PUT /rules/{id}` takes `saved_search_id` only.

## Changes

| File | Change |
|---|---|
| `internal/library/rules.go` | `RuleSpec.Filter`; `CreateRule` saves the search first when it's set; `CheckRuleSpec` accepts either |
| `internal/api/rules.go` | Preview of an unsaved filter |
| `web/src/cards/Cards.tsx` | Extract the sidebar's inputs (names and browsable keys) into a hook the wizard shares |
| `web/src/rules/RuleWizard.tsx` | New: the four steps, replacing `RuleEditor.tsx` |
| `web/src/rules/Rules.tsx` | Add rule button |
| `docs/using.md` | Adding a rule from the Rules page |

## Testing

- Core: a rule with `filter` saves its search and rule together; a taken
  name saves neither; `filter` and `saved_search_id` together, or neither,
  fail; preview of a `filter` saves nothing.
- Web: build a search, pick Archive and all libraries, name it, and save,
  which posts `filter`; pick a saved search instead, which posts its ID; a
  collect rule skips step 3; editing opens at step 4 with action and
  collection fixed; with no saved searches, step 1 opens on Build.
