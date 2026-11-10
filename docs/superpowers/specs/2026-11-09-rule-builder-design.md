# Rule builder

Add and edit a rule on one page that reads as a sentence, the way mail
rules do: conditions as rows of field, operator, and value, joined by and or
or, and grouped, then an action.
It replaces the four-step wizard. Builds on
[rules](2026-11-06-rules-design.md).

```
Name  [ Hide junk                         ]

When a card in  [All libraries ▾]  matches:
   Where   [Tag ▾] [is in ▾] (nsfw ✕) (gore ✕) [Add one ▾]       ✕
   [And ▾] [Quality ▾] [is at most ▾] [40]                       ✕
   and     [doesn't match ▾] group                               ✕
           ┌──────────────────────────────────────────┐
           │ Where   [Language ▾] [is ▾] [English ▾]  │
           │ [Or ▾]  [Source ▾] [is not ▾] [chub ▾]   │
           │ + Add condition   + Add group            │
           └──────────────────────────────────────────┘
   + Add condition   + Add group

then  [archive it ▾]                 (or: [add it to ▾] [Faves ▾])

   1,234 cards match in all libraries: Aria, Bob, Cass, …

                      [Cancel]  [Save]  [Save and turn on…]
```

## Decisions

| Question | Decision |
|---|---|
| Default rules | None. Plugins' shipped saved searches stay as they are. |
| Where rules start | Add rule on the Rules page opens `/rules/new`. Add rule on a saved search opens it with that search's conditions. |
| What the conditions are | The rule's saved search. A new rule's is saved under the rule's name. |
| Editing | `/rules/:id` loads the rule's conditions into rows. Action and collection stay fixed. |
| A search rows can't show | Its filter has a condition no field covers, such as a collection or app. It shows as one read-only row, "Matches saved search *name*", with Edit on Cards linking to `/cards?saved=<id>`. The rest of the page still works. Any and/or/not nesting shows as groups. |
| A search other rules use | Changing the conditions changes that search, so the page names the other rules under the conditions. |
| Live count | How many cards match across the rule's libraries, and the first few names, refreshed as conditions change. |
| Preview | Each library's totals and its first few names, with how many more. No paging. |
| Turning on | Save and turn on, or saving a rule that's on with changed conditions or libraries, shows the preview first. |

## Conditions

A group holds conditions and groups, joined by one connector, and or or.
Its first item reads Where; the second shows the connector as a menu, and
changing it changes the whole group, so mixing and with or takes a group.
The rest show it as text. A condition negates through its operator; a
group's matches / doesn't match menu wraps it in `not`. The page itself is
the outermost group, which can't be negated. Add group
works two levels deep; a deeper saved filter still shows and edits.

A group is an `and` or `or` of its items. Items that aren't complete are
left out, and a group left with none is too.

A condition is a field, an operator, and a value:

| Field | Operators | Value | Filter |
|---|---|---|---|
| Name | contains, doesn't contain | text | `name` |
| Creator | is, is not, is in, is not in | text | `creator` |
| Tag | is, is not, is in, is not in | a tag, offered with counts as you type | `tag` |
| Source | is, is not, is in, is not in | a source kind from the list | `source` |
| Shares a source | yes, no | (none) | `shares_source: true` |
| A score key | is at least, is at most, is between, has no score, has a score | one or two numbers | `enrichment` with `gte`, `lte`, or `missing` |
| A text or list key | is, is not, is in, is not in, has no value, has a value | a value, offered with counts | `enrichment` with `is` or `missing` |

A negative operator wraps its test in `not`. *Is in* is an `or` of one *is*
test per value, and *is not in* a `not` around that. Picking from a menu
adds a value to the list; typing one adds it on Enter, and a value still in
the box when saving counts too. Reading a filter back, a `not` of a test
with an opposite operator becomes that operator, an `or` of one field's *is*
tests becomes *is in*, and any other `not` becomes a group set to doesn't
match.

Score and value fields are the browsable keys the Cards sidebar lists, with
their labels, and carry `instance` when the key names one. Collection,
lorebook, and app conditions are left out: they belong to one library, and
most rules cover all of them.

## Saving

- **New rule:** `POST /saved-searches` with the rule's name and the rows'
  filter, then `POST /rules` on it. If the rule fails, the page deletes the
  search it just saved. A taken name shows inline.
- **Changed conditions:** `PUT /rules/{id}` (or `POST /rules` for a new rule
  on the search), then `PATCH /saved-searches/{id}` with the new filter, so
  a rule that fails to save leaves the shared search as it was. If the
  `PATCH` fails, the rule stays saved and an error toast says so.
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
| `web/src/rules/conditions.ts` (new) | Groups and conditions to filter and back, and which fields and operators exist |
| `web/src/rules/RuleBuilder.tsx` (new) | The page; replaces `RuleWizard.tsx` and `MatchStep.tsx` |
| `web/src/rules/RulePreview.tsx` | Passes `filter` through |
| `web/src/rules/Rules.css` | Row layout; drops the wizard's styles |
| `docs/using.md` | Adding a rule |

## Testing

- Core: preview with `filter` counts by it, not by the saved search's.
- Groups to filter and back, for every field and operator, with not on
  conditions and groups and nesting, and a filter rows can't show.
- Changing a group's connector changes the whole group; Add group stops two
  levels down.
- Build a rule from nothing and save: posts the search, then the rule; a
  failing rule deletes the search.
- Add rule on a saved search loads its conditions; editing them patches it.
- Editing a rule that's on with new conditions previews with `filter`
  before anything is saved.
- A search with a collection condition shows read-only with Edit on Cards;
  one with any-of groups, as the Cards page saves, edits as groups.
