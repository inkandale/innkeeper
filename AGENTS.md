# Agent guidelines

Rules for any AI agent writing code in this repo. The core is Go
(`cmd/`, `internal/`, `cardhash/`, `protocol/`); the web UI is React and
TypeScript (`web/src`). Setup and tests are in [CONTRIBUTING.md](CONTRIBUTING.md).

## Before you write code

1. Read the code the change touches and trace the real flow end to end.
2. Search for an existing helper, type, or pattern before writing a new one.
   Re-implementing something a few files over is the most common AI mistake
   here.
3. Ask whether the change solves a real problem now. Don't build for a
   hypothetical later.
4. Decide which test will prove the change, and write it first when you can.

## Plugins

- A plugin for a capability core already has (another target app, another
  catalog site) needs no core change.
- Something plugins couldn't do before, such as editing or a card studio,
  is a core feature: a new capability in `protocol/` that any plugin can
  offer. Build it once, through the shared code for loading, calling,
  checking, and listing plugins. Don't copy another capability's code in
  `internal/`, `web/src`, or the MCP tools; generalize the shared code.
- Core knows plugins only through the manifest and the protocol. Never
  branch on a plugin's name.
- Behavior specific to one app or one site belongs in its plugin.

## The live instance

The API token for https://innkeeper.pureroot.com is in `.innkeeper-token`
at the root of the main checkout, which git ignores. From any worktree:

```bash
export INNKEEPER_URL=https://innkeeper.pureroot.com
export INNKEEPER_TOKEN=$(cat "$(git rev-parse --git-common-dir)/../.innkeeper-token")
```

## SOLID, applied to Go

| Principle | What it means here |
|---|---|
| Single responsibility | A package, type, or function has one reason to change. A package's doc comment says what it owns; if the new code doesn't fit that sentence, it belongs elsewhere. |
| Open/closed | Extend through the seams that exist: plugins and job handlers (`jobs.Worker.Handlers`). Don't add a `switch` on kind in a third place; put the behavior with the kind. |
| Liskov substitution | Anything that satisfies an interface honors its whole contract, including errors and edge cases, not just the method signatures. |
| Interface segregation | Interfaces are small and defined by the consumer, like `querier` in `internal/jobs`. Don't define one next to its only implementation. |
| Dependency inversion | Pass dependencies in through constructors (`library.New(pool, blobs)`). No package-level mutable state or globals reached from deep inside. |

## DRY, without the wrong abstraction

- Extract shared code on the third copy, not the second. A little
  duplication is cheaper than the wrong abstraction.
- Duplicated knowledge is different from duplicated lines. A rule,
  constant, or format that must stay in sync lives in one place from the
  first copy.
- Fix a bug in the shared function every caller uses, not in the one caller
  the report names. Grep all callers first.
- The API schema is generated: edit Go, then run `npm run gen` in `web/`.
  Never hand-edit `web/openapi.json` or `web/src/api/schema.d.ts`.

## Clean code

**Naming**
- One name per concept across Go, the API, and the UI. Use the domain's
  words: card, lorebook, revision, target, plugin.
- No vague names: `data`, `info`, `manager`, `helper`, `util`, `handle2`.
- Names are greppable; avoid one-letter names outside short loops.

**Functions**
- A function does one thing, at one level of abstraction.
- Guard clauses and early returns instead of nested `if`/`else`.
- Three or more related parameters that travel together become a struct.
- A function's name covers all its side effects.

**Types**
- Give a domain value its own type when mixing it up would be a real bug,
  such as two kinds of ID passed side by side. Don't wrap every string.
- Keep data structs and behavior-bearing types distinct. Don't add methods
  to a wire struct that reach into the database.

**Errors**
- Return errors; don't log and continue. Wrap with context
  (`fmt.Errorf("load card %s: %w", id, err)`) and check with `errors.Is`.
- Package-level sentinel errors (`library.ErrNotFound`) for conditions
  callers branch on. No string matching on error text.
- Never swallow an error to make a test pass.

## Code smells to fix, not add

| Smell | Fix |
|---|---|
| Long function mixing levels | Extract the lower-level steps |
| Shotgun surgery: one change touches many files | Move the related code together |
| Feature envy: code mostly uses another package's data | Move it to that package |
| Repeated `switch` on the same kind | Put the behavior on the kind |
| Speculative generality: unused params, options, interfaces | Delete it |
| Dead code, commented-out code, unused exports | Delete it |

## Signs of sloppy AI code

Reject these in your own output:

- An interface, factory, or config option with one implementation or one
  value.
- A new dependency for something the standard library or an installed
  package already does.
- Wrappers that only forward to another function.
- Defensive checks for states the types already rule out.
- Comments that restate the code, narrate the change ("now uses X"), or
  describe the debugging session. Comment why, in one line.
- Tests that assert on mocks instead of behavior, or that pass whatever the
  code does.
- Unrelated edits: reformatting, renames, or refactors outside the task.
- Placeholder code: `TODO: implement`, stub returns, fake data in non-test
  code.

## UI feedback

Tell the user about an event in exactly one place:

| Use | When | How |
|---|---|---|
| Toast | The outcome of an action the user fires and moves on from: a confirmation, an Undo, a background job | `toast.success` from `sonner`; `toastJob` for a job; `toastError` for an error |
| Inline | The outcome belongs to a form or panel the user is still looking at: validation, save state, a connection test | An `.error` or `role="status"` element by the control, and `meta: { toast: false }` on the mutation |
| Banner | A state of the page, not an event: a conflict, a stale preview | `.banner`, shown until the state changes |
| Run header | Only while a forge run locks the page | `RunHeader`; a run that succeeds becomes a toast |

- Every failed mutation shows a toast unless it sets
  `meta: { toast: false }`. A mutation that shows its error inline must set
  it, or the error shows twice.
- Successes close themselves; errors stay until dismissed.

## Tests

- Test behavior through the public surface, not private helpers.
- Database code is tested against a real PostgreSQL, not mocks.
- Test names state a concrete case: `TestImportRejectsDuplicateSlug`, not
  `TestImport2`.
- Every bug fix comes with a test that fails without the fix.

## Before you call it done

Run what CI runs:

```bash
go vet ./...
go test -race ./...
cd web && npm run typecheck && npm test
```

Then read your diff once more for dead code, stale names, and anything the
task didn't ask for.
