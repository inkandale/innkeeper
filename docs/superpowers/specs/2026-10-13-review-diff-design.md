# Readable review diff

The sync review page (`web/src/sync/Review.tsx`) shows each differing hunk as two whole blocks, Library and App, with no mark on what changed. A one-character edit in a paragraph, or one key in a 40-line JSON value, has to be found by eye. Unchanged hunks sit between them in muted text with no label, so a stray `4` or `system` from `extensions` reads as noise.

This change is web-only. The server's comparison and its `words` spans stay as they are.

## Word diff

`sync.ts` gains `wordDiff(library, app)`. It returns the library and app text split into runs, each marked same, removed, or added.

- Tokens split the same way as the server's `words()` in `internal/merge/tokens.go`: each word keeps its trailing whitespace.
- The diff is a longest-common-subsequence over tokens. A hunk is a sentence run or one JSON value, so the table stays small: about 500 tokens a side at worst.
- It runs on the text the page shows (`show()`), so indented JSON diffs line by line as displayed.

The server's `Hunk.words` spans are not used. They are byte offsets into the server's compact encoding and exist only for prose conflicts, so they line up with neither the displayed JSON nor JavaScript string offsets.

## Layout by field kind

| Field kind | Layout |
|---|---|
| `prose`, and an `entries` hunk whose entry field is `content` | One column |
| `json`, `list`, `scalar`, other `entries` fields | Two columns, Library and App |

**One column.** Each hunk renders the library text with removed words struck through in red and added words in green, in reading order. A hunk present on one side only is all red or all green. The Library / App / Both choice sits under the block, unchanged.

**Two columns.** As today, with removed words highlighted on the Library side and added words on the App side. The Library and App headings appear once per field, not on every pair.

**Merged hunks.** `auto_app` and `auto_library` hunks keep their "App's change, merged:" / "Library's change, kept:" label and render as a word diff in the field's layout.

## Labels and unchanged runs

- Every hunk in a `json` field is labelled with its key path, taken from the hunk id minus the field path and the `#n` suffix. For example, `extensions.depth_prompt.depth#0` becomes `depth_prompt.depth`.
- In a prose field, more than two consecutive unchanged sentences collapse to a line such as "⋯ 3 unchanged sentences", which expands on click. Two or fewer stay visible as context.
- In a `json` field, unchanged keys collapse to one "N unchanged keys" line, which expands to the labelled values.

A collapsed run uses `<details>`, the same as each field does now.

## Styling

`Sync.css` only:

- `--diff-removed` and `--diff-added` background tints, defined for both themes beside the existing tokens.
- Removed runs get `text-decoration: line-through` as well as the tint, so the mark doesn't rely on color alone.
- A colored left border on each hunk that needs a decision.

## Tests

- `sync.test.ts`: `wordDiff` on a prose edit, a pure insertion, a pure deletion, and text with curly quotes and an em dash.
- `Review.test.tsx`: a prose hunk renders removed and added words in one column; a `json` hunk shows its key label; a run of three unchanged sentences collapses and expands.

## Out of scope

- Long prose stored inside a `json` field, such as `extensions.depth_prompt.prompt`, uses the two-column layout, since layout follows field kind.
- Card history (`web/src/card/History.tsx`) has its own hunk view and is unchanged.
