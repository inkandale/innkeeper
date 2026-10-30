# Card embeddings 1: never enrich archived cards

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** No enrich run sends an archived card to a plugin, whatever started it.

**Architecture:** Every enrich run loads cards through `Runner.heads` in `internal/enrich/enrich.go`. It drops archived cards there and counts them as skipped, so bulk runs, query runs and later automatic runs all follow the rule without knowing it.

**Tech stack:** Go, PostgreSQL.

**Spec:** [2026-10-30-card-embed-design.md](../specs/2026-10-30-card-embed-design.md), "Archived cards".

---

## Conventions

- Work in `~/git/innkeeper-card-embed` (branch `card-embed-spec`).
- Database tests need
  `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`
  (pgvector image, see `CONTRIBUTING.md`). Without it they skip, and a skip
  is not a pass: check the output says `ok` after real run time, not
  `0.3s`.
- Comments say why, in one line.

## Files

| File | Change |
|---|---|
| `internal/enrich/enrich.go` | `heads` drops archived cards; `batch` counts them as skipped |
| `internal/api/enrichments_test.go` | `TestEnrichRunSkipsArchivedCards` |

---

### Task 1: Skip archived cards in every enrich run

**Files:** `internal/enrich/enrich.go`, `internal/api/enrichments_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/api/enrichments_test.go`, after
`TestEnrichRunSkipsCardsScoredOnTheirHead`:

```go
func TestEnrichRunSkipsArchivedCards(t *testing.T) {
	e := newEnrichEnv(t)
	live, archived := e.card("Live", "xx"), e.card("Archived", "xx")
	e.call("PUT", fmt.Sprintf("/cards/%d/archived", archived), map[string]any{"archived": true}, 204, nil)

	j, p := e.evaluate(e.instID, live, archived)
	if j.Status != jobs.StatusSucceeded || p.Done != 1 || p.Skipped != 1 || p.Failed != 0 {
		t.Fatalf("job %s, progress %+v", j.Status, p)
	}
	var vals []enrichmentValue
	e.call("GET", fmt.Sprintf("/cards/%d/enrichments", archived), nil, 200, &vals)
	if len(vals) != 0 {
		t.Fatalf("archived card got values %+v", vals)
	}

	// A run of only archived cards succeeds with nothing sent, and so does a
	// query that asks for archived cards.
	j, p = e.evaluate(e.instID, archived)
	if j.Status != jobs.StatusSucceeded || p.Done != 0 || p.Skipped != 1 {
		t.Fatalf("only archived: job %s, progress %+v", j.Status, p)
	}
	j, p = e.startRun(map[string]any{"instance_id": e.instID, "query": map[string]any{"filter": map[string]any{"archived": true}}})
	if j.Status != jobs.StatusSucceeded || p.Done != 0 {
		t.Fatalf("archived query: job %s, progress %+v", j.Status, p)
	}
}
```

- [ ] **Step 2: Run it and see it fail**

```bash
INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres \
  go test ./internal/api -run TestEnrichRunSkipsArchivedCards -count=1
```

Expected: FAIL with `progress {... Done:2 ... Skipped:0 ...}`: today the
archived card is scored.

If the query run fails to start with a 422 because `filter` takes another
shape, read `library.Query` in `internal/library/search.go` and match it;
the point is a query whose filter selects archived cards.

- [ ] **Step 3: Drop archived cards in `heads`**

In `internal/enrich/enrich.go`, replace `heads`:

```go
// heads returns the head revision of each live card in ids, a warning for
// each one that doesn't exist, and how many were archived. Archived cards
// are never sent: archiving means the owner is done with the card.
func (rn *Runner) heads(ctx context.Context, ids []int64) (revs []protocol.EnrichRevision, missing []string, archived int) {
	for _, id := range ids {
		c, err := rn.Lib.Cards.Get(ctx, id)
		if err == nil && c.ArchivedAt != nil {
			archived++
			continue
		}
		var rev library.Revision
		if err == nil {
			rev, err = rn.Lib.Cards.Revision(ctx, c.HeadRevisionID)
		}
		if err != nil {
			missing = append(missing, fmt.Sprintf("card %d: %v", id, err))
			continue
		}
		revs = append(revs, protocol.EnrichRevision{CardID: id, RevisionID: rev.ID, Data: rev.Data})
	}
	return revs, missing, archived
}
```

- [ ] **Step 4: Count them in `batch`**

In `batch`, replace the opening lines through the `unscored` block:

```go
	instanceID := *r.InstanceID
	revs, missing, archived := rn.heads(ctx, ids)
	if len(revs) == 0 && archived == 0 {
		return Checked{}, errors.New("none of its cards exist")
	}
	skipped := archived
	if !p.Rescore {
		all := len(revs)
		var err error
		if revs, err = rn.unscored(ctx, instanceID, revs); err != nil {
			return Checked{}, err
		}
		skipped += all - len(revs)
	}
	if len(revs) == 0 {
		return Checked{Failed: len(missing), Skipped: skipped, Warnings: missing}, nil
	}
```

The empty-`revs` return moves out of the `!p.Rescore` block, so a rescore
of only archived cards also returns early instead of calling the plugin
with nothing.

- [ ] **Step 5: Run the new test and the package**

```bash
INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres \
  go test ./internal/api -run 'TestEnrichRun|TestStartEnrichRun' -count=1
INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres \
  go test ./internal/enrich/... -count=1
```

Expected: PASS, including `TestEnrichRunSkipsCardsScoredOnTheirHead`, whose
missing card 999999 must still count as failed.

- [ ] **Step 6: Commit**

```bash
git add internal/enrich/enrich.go internal/api/enrichments_test.go
git commit -m "fix(enrich): never send archived cards to an enrich plugin"
```

---

### Task 2: Full check

- [ ] **Step 1: Run what CI runs**

```bash
go vet ./...
INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./...
```

Expected: PASS. Then read the diff once more for anything the task didn't
ask for.
