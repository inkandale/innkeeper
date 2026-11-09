# Rule wizard

Add and edit rules from the Rules page in four steps, building the rule's
search there instead of on the Cards page first. Builds on
[rules](2026-11-06-rules-design.md). The web UI changes; the API doesn't.

## Decisions

| Question | Decision |
|---|---|
| Default rules | None. Plugins' shipped saved searches stay as they are: created with a new instance, and one choice among others in step 1. |
| Where rules start | Add rule on the Rules page opens the wizard at `/rules/new`. Add rule on a saved search opens it with that search picked. |
| Building a search | Step 1 embeds the Cards page's filter sidebar (`Facets`) on local state, not the URL. Search text isn't offered, since a saved search can't hold it. |
| Saving a built search | Step 1 ends by naming the search and saving it with `POST /saved-searches`. From then on the wizard holds its ID, as if it had been picked. Leaving the wizard later keeps the saved search. |
| Editing | `/rules/:id` opens the same wizard at step 4, every step reachable. Step 1 shows the rule's saved search and can switch to another; changing a search's filter stays on the Cards page (Update). Action and collection stay fixed. |
| Counts while building | Step 1 shows how many cards match in the current library, as the sidebar's counts do. Step 4's preview covers every library the rule would. |

## Steps

1. **Match.** Two choices:
   - **Build**, the default when there are no saved searches: the filter
     sidebar, the terms it picked as removable chips, the match count, and a
     name. Save and continue saves the search; a taken name shows inline.
   - **Saved search**: a list of saved searches. Next needs one picked.
2. **Do.** Archive, or add to a collection, with its library and collection.
3. **Where.** All libraries, new ones included, or the ones picked. A
   collect rule covers its collection's library alone, so this step is
   skipped.
4. **Review.** Rule name (required, the search's name to start), a summary
   of steps 1-3 with links back, and the preview. Save adds the rule off;
   Save and turn on adds it on after the preview is confirmed, as turning a
   rule on does today.

## Changes

| File | Change |
|---|---|
| `web/src/cards/Cards.tsx` | Extract the sidebar's inputs (names and browsable keys) into a hook the wizard shares |
| `web/src/rules/RuleWizard.tsx` | New: the four steps, replacing `RuleEditor.tsx` |
| `web/src/rules/Rules.tsx` | Add rule button |
| `docs/using.md` | Adding a rule from the Rules page |

## Testing

- Build a search, save it, pick Archive and all libraries, name the rule, and
  save: posts the saved search, then the rule with its ID.
- A taken search name shows inline and stays on step 1.
- Pick a saved search instead: posts only the rule.
- A collect rule skips step 3.
- Editing opens at step 4 with action and collection fixed.
- With no saved searches, step 1 opens on Build.
