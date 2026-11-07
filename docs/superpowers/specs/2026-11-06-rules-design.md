# Rules design

Date: 2026-11-06

## Purpose

Low-quality cards cost model time on every enrich instance and clutter
similar-card and search results. Today the only way to set them aside is a
bulk archive by hand, which goes stale as cards are imported and re-scored.

This spec adds two things:

- **Saved searches:** named, shared filters, such as "Junk" or "Needs
  improvement", that the owner opens to browse and act on.
- **Rules:** a saved search with an action that core keeps applied. An
  archive rule keeps junk out of every enrich queue; a collect rule keeps a
  collection in step with the search.

It also orders enrich instances by cost, so a cheap check can archive a card
before an expensive model sees it, and drops the vectors of archived cards.

It builds on [Browse sections](2026-11-06-browse-sections-design.md): the
saved-search editor and rule preview reuse its sectioned filter UI.

## Decisions

| Topic | Decision |
|---|---|
| Saved search | A name and a search filter. Top level, not owned by a library: opening one searches the library you're in. |
| Rule | A saved search, an action, a scope, and an on/off switch. |
| Actions | Archive and collect. A saved search can still filter on tags. There's no tag action, since tags live in a card's revision and tagging would write a new revision that every on-change plugin re-scores. |
| Live | A rule undoes its action on a card that stops matching. It never undoes what the owner did by hand. |
| Scope | All libraries, including ones made later, or a chosen list. A collect rule's scope is its collection's library only. |
| New rules | Start disabled. Enabling a rule, or editing an enabled one, shows the affected cards first and changes nothing until confirmed. |
| Defaults | A plugin's manifest can ship saved searches over its own keys. Core ships none, since no default should depend on a plugin the owner may not have. |
| Duplicates | Stay a library setting. They share the hide job's archived list with archive rules. |
| Enrich order | A plugin declares `enrich_cost`. An instance waits for cheaper instances that an archive rule reads. |
| Vectors | Archiving a card deletes its vectors. Un-archiving it gets it re-embedded. |
| Out of scope | Rule actions other than archive and collect. A per-instance override of `enrich_cost`. |

## Saved searches

### Storage

```sql
CREATE TABLE saved_search (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (name <> '' AND name = btrim(name)),
    filter jsonb NOT NULL,
    sort text NOT NULL DEFAULT '',
    instance_id bigint,
    created_at timestamptz NOT NULL DEFAULT now()
);
```

- `filter` is a `library.Filter`, checked by the search parser on save.
- `sort` is the sort to open the search with; rules ignore it.
- `instance_id` is set on a saved search a plugin shipped. Deleting the
  instance deletes its saved searches, in the delete handler rather than a
  foreign key: saved searches are backed up with the library and instances
  with config, as `enrichment.instance_id` already is. The owner can edit a
  shipped search like any other.
- `saved_search` and the rule tables join the library backup, with
  `DEFERRABLE INITIALLY IMMEDIATE` foreign keys, since restore defers
  constraints.

### API

`/saved-searches` with list, get, create, update and delete. A delete that
would orphan rules fails with 409 and names the rules.

### UI

- **Searching:** a Save search button on the library grid saves the current
  filter and sort under a name.
- **Saved searches page:** top level in the navigation. Each row shows the
  name, the match count in the current library, and the rules built on it.
  Opening one fills the library grid with its filter and sort.

### Plugin defaults

A manifest can declare saved searches over the plugin's own keys:

```toml
[searches.junk]
name = "Junk"
filter = { or = [
  { enrichment = "problems", is = "too_short" },
  { enrichment = "problems", is = "placeholder" },
  { enrichment = "problems", is = "no_first_message" },
  { enrichment = "problems", is = "no_description" },
] }
```

- When an instance is created, through the API or as a singleton at
  startup, core creates each one with the instance's ID filled into every
  enrichment term. A name already taken gets the instance label appended,
  such as "Junk (Card lint)". Plugin updates don't add or change them.
- Validation fails when a term names a key the plugin doesn't declare, or
  names an instance.
- Core creates the saved search only, never a rule.

This bumps `protocol.Minor` and adds a row to `docs/protocol.md`.

## Rules

### Storage

```sql
CREATE TABLE rule (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (name <> '' AND name = btrim(name)),
    saved_search_id bigint NOT NULL REFERENCES saved_search ON DELETE CASCADE,
    action text NOT NULL CHECK (action IN ('archive', 'collect')),
    collection_id bigint REFERENCES collection ON DELETE CASCADE,
    all_libraries boolean NOT NULL DEFAULT true,
    enabled boolean NOT NULL DEFAULT false,
    CHECK ((action = 'collect') = (collection_id IS NOT NULL))
);

CREATE TABLE rule_library (
    rule_id bigint NOT NULL REFERENCES rule ON DELETE CASCADE,
    library_id bigint NOT NULL REFERENCES library ON DELETE CASCADE,
    PRIMARY KEY (rule_id, library_id)
);

ALTER TABLE collection_card ADD COLUMN rule_id bigint REFERENCES rule ON DELETE CASCADE;
```

- `rule_library` holds the scope when `all_libraries` is false.
- A collect rule must have `all_libraries` false and exactly its
  collection's library in `rule_library`. The API enforces it.
- `rule_id` on `collection_card` marks a row a rule added. NULL means the
  owner added it. Deleting a rule deletes its rows.
- The saved-search cascade only covers restores and instance deletes: the API
  still refuses to delete a saved search that has rules.
- `card.archived_reason` is limited to `low_score` and `duplicate` today; the
  migration widens it to allow `rule:<id>`.

### Which cards a rule covers

A rule covers a library when it's enabled and its scope includes the library.
In each library it covers, it matches the cards its saved search's filter
matches there, archived cards included, so an un-archive is seen.

### Applying a rule

The hide job (`library.hide`) applies both actions, alongside its
duplicate work. It keeps its kind, so jobs queued before the deploy still run.
It runs per library.

- **Archive:**
  1. The job builds one archived list: the duplicates, then each archive
     rule's matches.
  2. `SetHidden` applies it as now.
  3. The reason is `rule:<id>` for a rule and `duplicate` for a duplicate. A
     card matching several archive rules takes the lowest rule ID.
  4. The card page shows the rule's name, such as "Archived by rule: Junk".
  5. Cards the owner archived (reason NULL) stay out of it, as now.
- **Collect:** the job inserts `collection_card (collection_id, card_id,
  rule_id)` for matching cards, and deletes the rule's rows for cards that no
  longer match. A card the owner already added keeps the owner's row and gets
  no rule row, since the primary key stays `(collection_id, card_id)`. An
  owner adding a card the rule added takes the row over.

An un-archive by the job sets `restored_at`, which `SetHidden` doesn't do
today, so the hourly enrich sweep sees the card as changed.

### When the job runs

| Event | Libraries |
|---|---|
| Import, or a move between libraries | The library the cards are in, as the hide job runs now |
| A rule created, enabled, disabled, edited or deleted | Every library in its scope before and after the change |
| A saved search edited | Every library each of its enabled rules covers |
| An enrich batch saves values | Only the cards in the batch; see [Enrich order](#enrich-order) |

The job keeps `EnqueueUnlessQueued`, so a burst of events queues one run per
library.

### Preview

`POST /rules/preview` takes a rule as it would be saved, enabled, and returns
for each library it would cover:

- How many cards it would archive or collect, and how many it would
  give back.
- The first page of each list, as search hits.

It catches filters that match more than intended. A `not` on an enrichment
term matches cards the instance hasn't scored, so a rule should pair it with
`missing = false`, as "Non-English" does.

The rule editor calls it before enabling or saving an enabled rule. The owner
confirms in a dialog that shows the counts and lets them page through the
cards. Nothing changes until they confirm.

### UI

- **Rules page:** top level in the navigation. Each row shows the name, the
  saved search, the action, the scope, the on/off switch, and how many cards
  it affects in each library it covers.
- **From a saved search:** an Add rule button starts a rule on that search.
- **Library settings:** list the rules that cover the library, each linking
  to the Rules page, under the existing duplicate settings.

## Enrich order

### Manifest

A top-level `enrich_cost` integer, beside `enrich_max_cards`. It defaults to 0
and needs the enrich capability. Space values apart, such as by 100, so later
plugins fit between:

| Plugin | `enrich_cost` |
|---|---|
| card-lint | 100 |
| crucible | 200 |
| forge-ai, card-embed | 300 |

This bumps `protocol.Minor` and adds a row to `docs/protocol.md`.

### Waiting

An instance **gates** a library when it has `enrich_on_change` on and an
enabled archive rule covering that library reads it: a term names the
instance, or names no instance and the instance is one the merged key reads.
An instance that never runs on its own can't gate, or cards would wait on it
forever.

For a card in library L, instance X **waits** while some instance G gates L,
G's cost is lower than X's, and G has neither a value nor a failure for the
card's head revision. A failure counts as done, so a broken gate can't hold
cards forever.

- **Sweep and on-change runs:** cards that wait are dropped from the batch
  and counted as waiting in the job's progress.
- **Manual runs:** don't wait. The owner chose the cards.
- **After a gate's batch:** the runner first runs the hide job's archive
  step over the batch's cards. It then queues an on-change run, for the
  cards still unarchived, on each instance that waited on the gate and has
  `enrich_on_change` on. The hourly sweep catches the rest.

Archived cards are already left out of the sweep and on-change runs.

## Vectors

- **On archive:** archiving a card for any reason, by the owner, a duplicate
  or a rule, deletes its rows in `card_embedding`. The card's enrichment
  values stay, since rules read them.
- **On un-archive:** for an instance that has stored a vector, the sweep and
  the runner's already-scored check count a card with no vector for its head
  revision as unenriched, even when it has that instance's enrichment values.
  Today both need the values missing too. An instance that declares a vector
  key but has never stored one, such as one with vectors turned off, isn't
  sent every card each hour.
- **On deploy:** the migration deletes the vectors of cards already archived.
  They can be rebuilt only by re-embedding.

## Testing

- **Archived list:** archive rules and duplicates share one archived list, and
  neither un-archives the other's cards.
- **Live undo:** a card that stops matching an archive rule comes back with
  `restored_at` set. One that stops matching a collect rule loses the rule's
  row and keeps an owner's row.
- **Tag filters:** a rule whose saved search filters by tag archives or
  collects the tagged cards.
- **Scope:** narrowing a rule's scope reruns the job in the library it left,
  and its cards there come back.
- **Collect scope:** a collect rule fails to save unless its scope is its
  collection's library.
- **Preview:** it returns the counts the job then applies, and changes
  nothing.
- **Saved-search delete:** deleting a saved search with rules on it fails with
  409.
- **Plugin defaults:** installing a plugin creates its saved searches with
  the instance ID filled in. Validation rejects a term naming an instance or
  an undeclared key.
- **Enrich order:** in a library with an archive rule on card-lint's keys,
  crucible waits until card-lint has a value or a failure, then gets only the
  cards the rule left unarchived. A manual run doesn't wait.
- **Vectors:** archiving deletes a card's vectors, and un-archiving it makes
  the sweep re-embed it.

## Plugin changes

In `innkeeper-plugins`:

| Plugin | Changes |
|---|---|
| card-lint | `enrich_cost = 100`. Ship saved searches "Junk" (the `problems` above) and "Non-English": `language` has a value and is neither English nor Unknown. |
| crucible | `enrich_cost = 200`. Ship saved search "Low effort" (`flags` is `low effort`). |
| forge-ai, card-embed | `enrich_cost = 300`. |

## On the live instance

After release, create the saved searches that read more than one plugin, as
the owner's own:

- **Best:** chub-archive `rating` 4 or more and `quality` 70 or more, sorted
  by `quality`.
- **Needs improvement:** chub-archive `rating` 4 or more and `quality` 40 or
  less, sorted by `rating`.

Set the thresholds from the score distributions at the time; these are
starting points.
