# Rule builder

Add and edit a rule on one page that reads as a sentence, the way mail
rules do: conditions as rows of field, operator, and value, then an action.
It replaces the four-step wizard. Builds on
[rules](2026-11-06-rules-design.md).

```
Name  [ Hide junk                         ]

When a card in  [All libraries ▾]  matches  [all ▾]  of:
   [Tag        ▾] [is        ▾] [nsfw      ▾]           ✕
   [Quality    ▾] [is below  ▾] [40         ]           ✕
   + Add condition

then  [archive it ▾]                 (or: [add it to ▾] [Faves ▾])

   1,234 cards match in Main · Aria, Bob, Cass, …

                      [Cancel]  [Save]  [Save and turn on…]
```

## Decisions

| Question | Decision |
|---|---|
| Default rules | None. Plugins' shipped saved searches stay as they are. |
| Where rules start | Add rule on the Rules page opens `/rules/new`. Add rule on a saved search opens it with that search's conditions. |
| What the conditions are | The rule's saved search. A new rule's is saved under the rule's name. |
| Editing | `/rules/:id` loads the rule's conditions into rows. Action and collection stay fixed. |
| A search rows can't show | Its filter isn't one level of conditions, as when built on Cards with any-of groups. It shows as one read-only row, "Matches saved search *name*", with Edit on Cards linking to `/cards?saved=<id>`. The rest of the page still works. |
| A search other rules use | Changing the conditions changes that search, so the page names the other rules under the conditions. |
| Live count | How many cards match in the current library, and the first few names, refreshed as conditions change. |
| Turning on | Save and turn on, or saving a rule that's on with changed conditions or libraries, shows the preview first. |

## Conditions

A row is a field, an operator, and a value. Match all is an `and` of the
rows, any an `or`. A negative operator wraps its condition in `not`.

| Field | Operators | Value | Filter |
|---|---|---|---|
| Name | contains | text | `name` |
| Creator | is, is not | text | `creator` |
| Tag | is, is not | a tag, offered with counts as you type | `tag` |
| Source | is, is not | a source kind from the list | `source` |
| Shares a source | (none) | (none) | `shares_source: true` |
| A score key | is at least, is at most, is between, has no score | one or two numbers | `enrichment` with `gte`, `lte`, or `missing` |
| A text or list key | is, is not, has no value | a value, offered with counts | `enrichment` with `is` or `missing` |

Score and value fields are the browsable keys the Cards sidebar lists, with
their labels, and carry `instance` when the key names one. Collection,
lorebook, and app conditions are left out: they belong to one library, and
most rules cover all of them.

## Saving

- **New rule:** `POST /saved-searches` with the rule's name and the rows'
  filter, then `POST /rules` on it. If the rule fails, the page deletes the
  search it just saved. A taken name shows inline.
- **Changed conditions:** `PATCH /saved-searches/{id}` with the new filter,
  then `PUT /rules/{id}`.
- **Save and turn on** on a new rule: preview the unsaved rule with
  `filter`, then on confirm save it as above and turn it on with `PUT`.
  Cancelling saves nothing. If turning it on fails, the rule stays, off, and
  an error toast says so.

## API

`POST /rules/preview` takes an optional `filter`. With it, the preview reads
that filter in place of the rule's saved search's, and `saved_search_id`
may be 0, so a new rule or an edit can be previewed before anything is
saved. Nothing else changes.

## Changes

| File | Change |
|---|---|
| `internal/api/rules.go` | Preview takes `filter` |
| `web/src/rules/conditions.ts` (new) | Rows to filter and back, and which fields and operators exist |
| `web/src/rules/RuleBuilder.tsx` (new) | The page; replaces `RuleWizard.tsx` and `MatchStep.tsx` |
| `web/src/rules/RulePreview.tsx` | Passes `filter` through |
| `web/src/rules/Rules.css` | Row layout; drops the wizard's styles |
| `docs/using.md` | Adding a rule |

## Testing

- Core: preview with `filter` counts by it, not by the saved search's.
- Rows to filter and back, for every field and operator, and a filter rows
  can't show.
- Build a rule from nothing and save: posts the search, then the rule; a
  failing rule deletes the search.
- Add rule on a saved search loads its conditions; editing them patches it.
- Editing a rule that's on with new conditions previews with `filter`
  before anything is saved.
- A search with any-of groups shows read-only with Edit on Cards.
